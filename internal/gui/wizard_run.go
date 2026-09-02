package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/app"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

func readStatusDepth(path string) (int, error) {
	s, err := health.ReadStatus(path)
	if err != nil {
		return 0, err
	}
	return int(s.QueueDepth), nil
}

// wizardCfg 는 마법사가 읽는 최소 설정 뷰다.
type wizardCfg struct {
	APIHost string
	DataDir string
	cfg     *config.Config
}

func (c *wizardCfg) readerAddr(id string) string {
	if r, ok := c.cfg.Reader(id); ok {
		return r.Addr
	}
	return ""
}
func (c *wizardCfg) readerToken(id string) (domain.Secret, bool) {
	r, ok := c.cfg.Reader(id)
	if !ok {
		return domain.Secret{}, false
	}
	return r.Token, true
}

func loadCfgForWizard(path string) (*wizardCfg, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &wizardCfg{APIHost: cfg.APIHost, DataDir: cfg.DataDir, cfg: cfg}, nil
}

func joinWarns(w []string) string { return strings.Join(w, " · ") }

// sendEvent 는 관측된 체크인 결과다.
type sendEvent struct {
	reader string
	status int
}

// watchSendResults 는 코어 로그에서 SEND_RESULT 를 관측한다. 호스팅 모드는
// LogRing, 그 외는 dataDir 로그 tail 을 쓴다. 마법사 3a 전용이라 간단히
// 로그 파일 tail 로 통일한다(호스팅도 파일에 기록됨).
func (w *Wizard) watchSendResults(ctx context.Context) <-chan sendEvent {
	out := make(chan sendEvent, 8)
	dir := filepath.Join(w.env.dataDir(), "logs")
	go func() {
		f := latestLogFile(dir)
		var offset int64
		if fi, err := os.Stat(f); err == nil {
			offset = fi.Size() // 현재 끝부터 (과거 결과 제외)
		}
		poll := time.NewTicker(300 * time.Millisecond)
		defer poll.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
			}
			nf := latestLogFile(dir)
			if nf != f {
				f, offset = nf, 0
			}
			data, err := os.ReadFile(f)
			if err != nil || int64(len(data)) <= offset {
				continue
			}
			chunk := data[offset:]
			offset = int64(len(data))
			for _, line := range strings.Split(string(chunk), "\n") {
				if !strings.Contains(line, `"SEND_RESULT"`) {
					continue
				}
				var rec struct {
					Event      string `json:"event"`
					ReaderID   string `json:"readerId"`
					HTTPStatus int    `json:"httpStatus"`
				}
				if json.Unmarshal([]byte(line), &rec) == nil && rec.Event == "SEND_RESULT" {
					select {
					case out <- sendEvent{rec.ReaderID, rec.HTTPStatus}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return out
}

// selftestReplay 는 {dataDir}_selftest 격리 환경에서 미등록 EPC 1건을
// 재생해 파싱→큐→HTTP→분류 전 구간을 검증한다 (§5.3 3b·4, §5.5). 대상
// 리더 1개만 포함한 사본 config 를 쓰고, 미등록 EPC 라 서버에 체크인
// 레코드를 만들지 않는다. offline=true 면 도달 불가 apiHost 로 큐 적재만
// 확인한다.
func (w *Wizard) selftestReplay(ctx context.Context, cfg *wizardCfg, readerID string, offline bool) StepResult {
	r, ok := cfg.cfg.Reader(readerID)
	if !ok {
		return StepResult{Status: StepFail, Detail: "리더를 찾을 수 없습니다"}
	}
	selfDir := strings.TrimRight(cfg.DataDir, `/\`) + "_selftest"
	os.RemoveAll(selfDir)
	os.MkdirAll(selfDir, 0o755)
	defer os.RemoveAll(selfDir)

	apiHost := cfg.APIHost
	if offline {
		apiHost = "http://127.0.0.1:1" // 도달 불가
	}
	// 대상 리더 1개만 포함한 사본 config
	sub := map[string]any{
		"version": 1, "apiHost": apiHost, "dataDir": selfDir,
		"debounceSec": 1, "queueMaxAgeHours": 24, "requestTimeoutSec": 5,
		"powerGain": cfg.cfg.PowerGain, "buzzer": 0, "logLevel": "info",
		"readers": []map[string]any{{"id": r.ID, "addr": r.Addr, "pulseToken": r.Token.Raw()}},
	}
	subBytes, _ := json.Marshal(sub)
	subCfg, err := config.Parse(bytesReader(subBytes))
	if err != nil {
		return StepResult{Status: StepFail, Detail: "격리 설정 생성 실패: " + err.Error()}
	}

	// 미등록 EPC 합성 라인 (실장비 PC=3400 계열)
	epc := "3400E200470D000000000000FEEDFACE"
	input := strings.NewReader(">T" + epc + "\r\n")

	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runErr := app.Run(rctx, app.Options{
		Cfg: subCfg, ReplayInput: input, ReplayReader: r.ID,
		DrainAndExit: true, Mode: "cli",
	})
	_ = runErr

	// _selftest 로그에서 결과 판정
	status, class := lastSelftestResult(filepath.Join(selfDir, "logs"))
	if offline {
		// 오프라인: 전송 실패로 큐에 남아야 정상
		depth := selftestQueueDepth(selfDir)
		if depth > 0 || class == "NETWORK_FAILURE" {
			return StepResult{Status: StepPass, Detail: "인터넷 차단 시 큐에 안전하게 보관됨",
				Metrics: map[string]string{"대기": fmt.Sprintf("%d건", depth)}}
		}
		return StepResult{Status: StepWarn, Detail: "오프라인 큐 적재를 확인하지 못했습니다"}
	}
	switch status {
	case 404:
		return StepResult{Status: StepPass, Detail: "전송 경로 정상 — 미등록 태그는 무시(404) 후 계속",
			Metrics: map[string]string{"분류": class}}
	case 0:
		return StepResult{Status: StepWarn, Detail: "전송 결과를 관측하지 못했습니다(타임아웃)"}
	default:
		return StepResult{Status: StepWarn,
			Detail: fmt.Sprintf("예상과 다른 응답(HTTP %d, %s) — 서버에 미등록 EPC 가 등록돼 있을 수 있음", status, class)}
	}
}

func lastSelftestResult(logDir string) (int, string) {
	f := latestLogFile(logDir)
	data, err := os.ReadFile(f)
	if err != nil {
		return 0, ""
	}
	status, class := 0, ""
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, `"SEND_RESULT"`) {
			continue
		}
		var rec struct {
			HTTPStatus  int    `json:"httpStatus"`
			ResultClass string `json:"resultClass"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil {
			status, class = rec.HTTPStatus, rec.ResultClass
		}
	}
	return status, class
}

func selftestQueueDepth(dir string) int {
	// status.json 의 queueDepth 로 근사
	s, err := readStatusDepth(filepath.Join(dir, "status.json"))
	if err != nil {
		return 0
	}
	return s
}
