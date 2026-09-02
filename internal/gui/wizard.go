package gui

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/reader/session"
)

// Wizard 는 현장 점검 마법사다 (GUI 설계 §5). 운영 큐·gate 를 건드리지
// 않으며(불변식), 실서버 체크인을 만드는 3a 는 confirm 관문 뒤에만 진행한다.

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepPass    StepStatus = "pass"
	StepWarn    StepStatus = "warn"
	StepFail    StepStatus = "fail"
	StepSkip    StepStatus = "skip"
)

type StepResult struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Status  StepStatus `json:"status"`
	Detail  string     `json:"detail"`
	Action  string     `json:"action,omitempty"` // 실패 시 조치 안내
	Metrics map[string]string `json:"metrics,omitempty"`
}

type WizardState struct {
	Running    bool         `json:"running"`
	AwaitTag   bool         `json:"awaitTag"`   // 3a 확인 후 태그 스캔 대기 중
	Steps      []StepResult `json:"steps"`
	StartedAt  string       `json:"startedAt,omitempty"`
	FinishedAt string       `json:"finishedAt,omitempty"`
}

// wizardEnv 는 마법사가 코어에 접근하는 통로다 — guiApp 이 채운다.
type wizardEnv struct {
	cfgPath     string
	catalogPath func() string
	dataDir     func() string
	coreRunning func() bool
	// readerSession 은 리더의 현재 connState 를 돌려준다 (코어 점유 시 대체 판정).
	readerConn func(readerID string) string
}

type Wizard struct {
	env wizardEnv

	mu       sync.Mutex
	state    WizardState
	cancel   context.CancelFunc
	tagCh    chan struct{} // 3a confirm 신호
	onChange func()
}

func NewWizard(env wizardEnv, onChange func()) *Wizard {
	return &Wizard{env: env, onChange: onChange}
}

func (w *Wizard) Snapshot() WizardState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *Wizard) setStep(i int, s StepResult) {
	w.mu.Lock()
	if i < len(w.state.Steps) {
		w.state.Steps[i] = s
	}
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange()
	}
}

// Start 는 선택된 단계들을 순서대로 실행한다. readerID 는 1·2·3 단계 대상.
func (w *Wizard) Start(readerID string, steps []string) *apiError {
	w.mu.Lock()
	if w.state.Running {
		w.mu.Unlock()
		return &apiError{"conflict_busy", "점검이 이미 실행 중입니다"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.tagCh = make(chan struct{}, 1)
	w.state = WizardState{Running: true, StartedAt: time.Now().Format(time.RFC3339)}
	for _, id := range steps {
		w.state.Steps = append(w.state.Steps, StepResult{ID: id, Title: stepTitle(id), Status: StepPending})
	}
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange()
	}
	go w.run(ctx, readerID, steps)
	return nil
}

func (w *Wizard) Abort() {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
}

// ConfirmTag 는 3a 실체크인 관문 통과 신호다 (§5.1 b).
func (w *Wizard) ConfirmTag() {
	w.mu.Lock()
	ch := w.tagCh
	w.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func stepTitle(id string) string {
	switch id {
	case "0":
		return "설정·환경 점검"
	case "1":
		return "리더 연결"
	case "2":
		return "서버 확인 (preflight)"
	case "3a":
		return "실태그 체크인"
	case "3b":
		return "무해 전송 시험 (미등록 태그)"
	case "4":
		return "오프라인 차단→복구"
	default:
		return id
	}
}

func (w *Wizard) run(ctx context.Context, readerID string, steps []string) {
	defer func() {
		w.mu.Lock()
		w.state.Running = false
		w.state.AwaitTag = false
		w.state.FinishedAt = time.Now().Format(time.RFC3339)
		w.cancel = nil
		w.mu.Unlock()
		if w.onChange != nil {
			w.onChange()
		}
	}()

	cfg, cfgErr := loadCfgForWizard(w.env.cfgPath)
	preflightOK := false

	for i, id := range steps {
		if ctx.Err() != nil {
			return
		}
		w.setStep(i, StepResult{ID: id, Title: stepTitle(id), Status: StepRunning})
		var res StepResult
		switch id {
		case "0":
			res = w.step0(cfg, cfgErr)
		case "1":
			res = w.step1(ctx, cfg, readerID)
		case "2":
			res = w.step2(ctx, cfg, readerID)
			preflightOK = res.Status == StepPass
		case "3a":
			if !preflightOK {
				res = skip(id, "서버 확인(2단계)이 통과하지 못해 건너뜁니다")
			} else {
				res = w.step3a(ctx, readerID, i)
			}
		case "3b":
			if !preflightOK {
				res = skip(id, "서버 확인(2단계)이 통과하지 못해 건너뜁니다")
			} else {
				res = w.step3b(ctx, cfg, readerID)
			}
		case "4":
			res = w.step4(ctx, cfg, readerID)
		default:
			res = StepResult{ID: id, Title: id, Status: StepSkip, Detail: "알 수 없는 단계"}
		}
		res.ID, res.Title = id, stepTitle(id)
		w.setStep(i, res)
	}
}

func skip(id, why string) StepResult {
	return StepResult{ID: id, Status: StepSkip, Detail: why}
}

// ─── 단계 0: 설정·환경 ───
func (w *Wizard) step0(cfg *wizardCfg, cfgErr error) StepResult {
	if cfgErr != nil {
		return StepResult{Status: StepFail, Detail: "설정을 읽을 수 없습니다: " + cfgErr.Error(),
			Action: "설정 탭에서 config 를 만들거나 고치세요"}
	}
	m := map[string]string{}
	warns := []string{}
	// dataDir 쓰기
	if err := health.CheckDiskDir(cfg.DataDir); err != nil {
		return StepResult{Status: StepFail, Detail: "데이터 폴더에 쓸 수 없습니다: " + err.Error(),
			Action: "설정의 데이터 폴더 경로·권한을 확인하세요", Metrics: m}
	}
	// NTP
	ntp := health.CheckNTP()
	if ntp.Supported && (!ntp.ServiceRunning || !ntp.QueryOK) {
		warns = append(warns, "Windows 시간 동기화(NTP)가 확인되지 않음")
	}
	// 서버 Date 기반 skew
	client, err := congkong.New(cfg.APIHost, 10*time.Second)
	if err == nil {
		if skew, ok := client.ProbeSkew(context.Background()); ok {
			sec := int(skew.Seconds())
			m["시계오차(초)"] = fmt.Sprintf("%d", sec)
			if sec < 0 {
				sec = -sec
			}
			if sec >= 240 {
				return StepResult{Status: StepFail,
					Detail: fmt.Sprintf("PC 시계가 서버와 %d초 어긋남 — 체크인이 거부될 수 있음", sec),
					Action: "Windows 시간 동기화(w32tm /resync)를 실행하세요", Metrics: m}
			}
		}
	}
	// 카탈로그 파일
	if _, err := os.Stat(w.env.catalogPath()); err != nil {
		warns = append(warns, "세션 카탈로그 파일 없음(세션 선택 불가)")
	}
	if len(warns) > 0 {
		return StepResult{Status: StepWarn, Detail: joinWarns(warns), Metrics: m}
	}
	return StepResult{Status: StepPass, Detail: "설정·데이터 폴더·시계 정상", Metrics: m}
}

// ─── 단계 1: 리더 연결 ───
func (w *Wizard) step1(ctx context.Context, cfg *wizardCfg, readerID string) StepResult {
	addr := cfg.readerAddr(readerID)
	if addr == "" {
		return StepResult{Status: StepFail, Detail: "리더를 찾을 수 없습니다: " + readerID}
	}
	// 코어가 리더 점유 중이면 직접 Probe 불가 — connState 로 대체 판정 (§5.3 #1)
	if w.env.coreRunning() {
		switch w.env.readerConn(readerID) {
		case "CONNECTED":
			return StepResult{Status: StepPass, Detail: "수집 중 — 리더 연결 정상(운영 상태로 판정)"}
		default:
			return StepResult{Status: StepFail,
				Detail: "수집 중이지만 리더가 연결되지 않음",
				Action: "리더 전원·케이블을 확인하고, 다른 프로그램이 리더에 접속해 있으면 종료 후 전원을 재투입하세요"}
		}
	}
	// 코어 미실행 — 직접 Probe
	pr, err := session.Probe(ctx, addr, nil)
	if err != nil {
		return StepResult{Status: StepFail, Detail: err.Error(),
			Action: "리더 전원·케이블 확인, 다른 프로그램(YAT 등) 점유 시 종료 후 전원 재투입"}
	}
	m := map[string]string{"펌웨어": pr.Firmware, "응답(ms)": fmt.Sprintf("%d", pr.RTT.Milliseconds())}
	if pr.RTT > 4*time.Second {
		return StepResult{Status: StepWarn, Detail: "연결되나 응답이 느림", Metrics: m}
	}
	return StepResult{Status: StepPass, Detail: "리더 연결·초기화 정상", Metrics: m}
}

// ─── 단계 2: 서버 preflight (no-op 저장, 운영 gate 불변) ───
func (w *Wizard) step2(ctx context.Context, cfg *wizardCfg, readerID string) StepResult {
	token, ok := cfg.readerToken(readerID)
	if !ok {
		return StepResult{Status: StepFail, Detail: "리더 토큰이 없습니다 — 세션을 먼저 선택하세요"}
	}
	client, err := congkong.New(cfg.APIHost, 10*time.Second)
	if err != nil {
		return StepResult{Status: StepFail, Detail: err.Error()}
	}
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	res := client.Preflight(cctx, token)
	if res.TransportErr != nil {
		return StepResult{Status: StepFail, Detail: "서버에 연결할 수 없습니다: " + res.TransportErr.Error(),
			Action: "인터넷 연결을 확인하세요"}
	}
	if res.Status == 404 {
		return StepResult{Status: StepFail, Detail: "토큰이 회수되었거나 무효입니다(404)",
			Action: "콘솔에서 토큰을 재발급하고 카탈로그를 갱신하세요"}
	}
	if res.Status != 200 {
		return StepResult{Status: StepFail, Detail: fmt.Sprintf("서버 오류 (HTTP %d)", res.Status)}
	}
	meta, ok := congkong.PreflightMeta(res.Body)
	if !ok {
		return StepResult{Status: StepFail, Detail: "서버 응답 형식 오류"}
	}
	m := map[string]string{"부스": meta.BoothName, "유닛": meta.UnitName, "쿨다운(초)": fmt.Sprintf("%d", meta.CooldownSec)}
	if meta.CooldownSec <= 0 {
		return StepResult{Status: StepWarn, Detail: "연결되나 중복 방지(쿨다운)가 꺼져 있음",
			Action: "운영진에게 쿨다운 설정을 요청하세요", Metrics: m}
	}
	return StepResult{Status: StepPass, Detail: fmt.Sprintf("서버 확인 완료 — %s / %s", meta.BoothName, meta.UnitName), Metrics: m}
}

// ─── 단계 3a: 실태그 체크인 관측 ───
func (w *Wizard) step3a(ctx context.Context, readerID string, stepIdx int) StepResult {
	if !w.env.coreRunning() {
		return StepResult{Status: StepSkip,
			Detail: "수집이 실행 중이 아니라 실태그 관측을 건너뜁니다 — 수집 시작 후 다시 시도하세요"}
	}
	// confirm 관문 (§5.1 b)
	w.mu.Lock()
	w.state.AwaitTag = true
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange()
	}
	select {
	case <-ctx.Done():
		return StepResult{Status: StepSkip, Detail: "중단됨"}
	case <-w.tagCh:
	case <-time.After(2 * time.Minute):
		w.clearAwait()
		return StepResult{Status: StepSkip, Detail: "확인이 없어 건너뜀"}
	}
	w.clearAwait()

	// 코어 로그(SEND_RESULT)에서 관측 — 호스팅은 LogRing, 관측 모드는 로그 tail
	deadline := time.After(60 * time.Second)
	watch := w.watchSendResults(ctx)
	for {
		select {
		case <-ctx.Done():
			return StepResult{Status: StepSkip, Detail: "중단됨"}
		case <-deadline:
			return StepResult{Status: StepFail,
				Detail: "60초 안에 체크인 결과를 관측하지 못했습니다",
				Action: "태그를 리더 앞에 확실히 대고 다시 시도하세요"}
		case ev := <-watch:
			if ev.reader != "" && ev.reader != readerID {
				continue
			}
			switch ev.status {
			case 200:
				return StepResult{Status: StepPass, Detail: "실태그 체크인 성공 (HTTP 200)",
					Metrics: map[string]string{"결과": "성공"}}
			case 409:
				return StepResult{Status: StepWarn, Detail: "중복 체크인으로 관측됨 (409) — 이미 체크인된 태그",
					Metrics: map[string]string{"결과": "중복"}}
			case 404:
				return StepResult{Status: StepFail, Detail: "페어링되지 않은 태그입니다 (404)",
					Action: "콘솔에서 이 참가자에게 태그를 페어링하세요"}
			default:
				return StepResult{Status: StepFail, Detail: fmt.Sprintf("체크인 실패 (HTTP %d)", ev.status)}
			}
		}
	}
}

func (w *Wizard) clearAwait() {
	w.mu.Lock()
	w.state.AwaitTag = false
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange()
	}
}

// ─── 단계 3b·4: _selftest 격리 재생 ───
func (w *Wizard) step3b(ctx context.Context, cfg *wizardCfg, readerID string) StepResult {
	return w.selftestReplay(ctx, cfg, readerID, false)
}

func (w *Wizard) step4(ctx context.Context, cfg *wizardCfg, readerID string) StepResult {
	return w.selftestReplay(ctx, cfg, readerID, true)
}
