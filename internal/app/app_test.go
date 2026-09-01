package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	_ "modernc.org/sqlite"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeAPI 는 preflight GET 과 check-in POST 를 처리하고 체크인을 기록한다.
type fakeAPI struct {
	mu       sync.Mutex
	checkins []checkinReq
}

type checkinReq struct {
	EPC       string
	CheckedAt string
}

func (f *fakeAPI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"eventName":"테스트 행사","boothName":"A 게이트","unitName":"본관","action":"in","cooldownSec":60}`))
			return
		}
		var body struct {
			BarcodeString string `json:"barcodeString"`
			CheckedAt     string `json:"checkedAt"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.checkins = append(f.checkins, checkinReq{EPC: body.BarcodeString, CheckedAt: body.CheckedAt})
		f.mu.Unlock()
		// PII 포함 응답 — 미들웨어가 절대 로그하면 안 되는 내용 (B7)
		w.Write([]byte(`{"result":"success","action":"in","checkedAt":"2026-09-01T10:00:00+09:00",
		  "attendee":{"fullName":"홍길동","membershipName":"콩콩","membershipPosition":"매니저",
		  "mobileNumber":"01098765432","emailAddress":"hong@congkong.example"}}`))
	}
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checkins)
}

func (f *fakeAPI) all() []checkinReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]checkinReq, len(f.checkins))
	copy(out, f.checkins)
	return out
}

func testConfig(t *testing.T, apiHost, dataDir string) *config.Config {
	t.Helper()
	raw := fmt.Sprintf(`{
	  "version": 1,
	  "apiHost": %q,
	  "dataDir": %q,
	  "readers": [{"id":"gate-a","addr":"127.0.0.1:1","pulseToken":%q}]
	}`, apiHost, dataDir, testToken)
	cfg, err := config.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// ndjsonFixture 는 receivedAt 간격을 재현하는 재생 입력을 만든다.
func ndjsonFixture(base time.Time, lines []string, interval time.Duration) string {
	var sb strings.Builder
	for i, line := range lines {
		chunk := hex.EncodeToString([]byte(line + "\r\n"))
		rec := fmt.Sprintf(`{"receivedAt":%q,"chunks":[%q]}`,
			base.Add(time.Duration(i)*interval).Format(time.RFC3339), chunk)
		sb.WriteString(rec + "\n")
	}
	return sb.String()
}

// B2: 같은 UID 1초 간격 100회 → 서버 요청 최대 2건
func TestAcceptanceB2ReplayHundredScans(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()
	dataDir := t.TempDir()
	cfg := testConfig(t, srv.URL, dataDir)

	lines := make([]string, 100)
	for i := range lines {
		lines[i] = ">T3000E28068940000501EC2205F7B"
	}
	// 재생 시각은 과거가 아니라 최근이어야 24h 만료에 걸리지 않는다.
	base := time.Now().Add(-100 * time.Second)
	input := ndjsonFixture(base, lines, time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := Run(ctx, Options{
		Cfg: cfg, ReplayInput: strings.NewReader(input),
		ReplayNDJSON: true, ReplayReader: "gate-a", DrainAndExit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := api.count(); n > 2 || n < 1 {
		t.Errorf("서버 요청 %d건 — 최대 2건이어야 함 (B2)", n)
	}

	// B7: 로그 corpus 에 PII·전체 토큰이 한 글자도 없어야 함
	assertNoPIIInLogs(t, dataDir)
}

func assertNoPIIInLogs(t *testing.T, dataDir string) {
	t.Helper()
	logs, err := filepath.Glob(filepath.Join(dataDir, "logs", "*.jsonl"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("로그 파일 없음: %v", err)
	}
	forbidden := []string{"홍길동", "01098765432", "hong@congkong.example", testToken, "membershipName"}
	for _, path := range logs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range forbidden {
			if strings.Contains(string(b), word) {
				t.Errorf("%s 에 금지 문자열 %q 포함 (B7/R8 위반)", filepath.Base(path), word)
			}
		}
	}
	// status.json 도 검사
	if b, err := os.ReadFile(filepath.Join(dataDir, "status.json")); err == nil {
		for _, word := range forbidden {
			if strings.Contains(string(b), word) {
				t.Errorf("status.json 에 금지 문자열 %q 포함", word)
			}
		}
	}
}

// B3: 네트워크 차단 → 5건 스캔 → 재시작 → 복구 → 원래 checkedAt 으로 5건 전송
func TestAcceptanceB3OfflineRestartRecovery(t *testing.T) {
	// 포트를 예약해 두고 phase 1 에서는 닫아 connection refused 를 만든다.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	apiHost := "http://" + addr
	dataDir := t.TempDir()

	epcs := []string{"AAAA0001", "AAAA0002", "AAAA0003", "AAAA0004", "AAAA0005"}
	lines := make([]string, len(epcs))
	for i, e := range epcs {
		lines[i] = ">T3000" + e
	}
	// fixture 의 receivedAt 은 RFC3339(초 단위)이므로 기대값도 같은 정밀도로 맞춘다.
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	input := ndjsonFixture(base, lines, time.Second)

	// --- phase 1: 서버 없음, 5건 적재 후 종료 (프로세스 재시작 시뮬레이션) ---
	cfg := testConfig(t, apiHost, dataDir)
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() {
		done1 <- Run(ctx1, Options{
			Cfg: cfg, ReplayInput: strings.NewReader(input),
			ReplayNDJSON: true, ReplayReader: "gate-a", DrainAndExit: false,
		})
	}()
	// 5건이 큐에 영속화될 때까지 대기
	waitFor(t, 15*time.Second, func() bool {
		return queueDepth(t, dataDir) == 5
	}, "5건 적재")
	cancel1()
	if err := <-done1; err != nil {
		t.Fatal(err)
	}

	// --- phase 2: 서버 복구 + 재기동 → 원래 checkedAt 으로 재생 ---
	api := &fakeAPI{}
	l2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: l2, Config: &http.Server{Handler: api.handler()}}
	srv.Start()
	defer srv.Close()

	cfg2 := testConfig(t, apiHost, dataDir)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		done2 <- Run(ctx2, Options{
			Cfg: cfg2, ReplayInput: strings.NewReader(""),
			ReplayReader: "gate-a", DrainAndExit: false,
		})
	}()
	waitFor(t, 20*time.Second, func() bool { return api.count() >= 5 }, "5건 재생")
	cancel2()
	if err := <-done2; err != nil {
		t.Fatal(err)
	}

	got := api.all()
	if len(got) != 5 {
		t.Fatalf("전송 %d건, want 5", len(got))
	}
	wantAt := map[string]string{}
	for i, e := range epcs {
		wantAt[e] = base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
	}
	for _, c := range got {
		want, ok := wantAt[c.EPC]
		if !ok {
			t.Errorf("예상 밖 EPC: %s", c.EPC)
			continue
		}
		gotT, err1 := time.Parse(time.RFC3339Nano, c.CheckedAt)
		wantT, err2 := time.Parse(time.RFC3339Nano, want)
		if err1 != nil || err2 != nil || !gotT.Equal(wantT) {
			t.Errorf("%s: checkedAt %q, want %q (원래 시각 보존 위반)", c.EPC, c.CheckedAt, want)
		}
	}
	if d := queueDepth(t, dataDir); d != 0 {
		t.Errorf("복구 후 큐 잔량 %d", d)
	}
}

// queueDepth 는 실행 중인 앱과 잠금 경합 없이 큐 길이를 읽는다.
// store/sqlite.Open 은 PRAGMA/migration 을 다시 실행해 writer 와 경합하므로
// 테스트에서는 read-only 연결로 조회한다.
func queueDepth(t *testing.T, dataDir string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "queue.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Logf("queueDepth open: %v", err)
		return -1
	}
	defer db.Close()
	var d int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM queue_items`).Scan(&d); err != nil {
		t.Logf("queueDepth query: %v", err)
		return -1
	}
	return d
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s 대기 timeout", what)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// 이중 실행 방지 (설계서 §7.1)
func TestSecondInstanceRejected(t *testing.T) {
	dataDir := t.TempDir()
	release, err := acquireLock(filepath.Join(dataDir, "app.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireLock(filepath.Join(dataDir, "app.lock")); err == nil {
		t.Fatal("이중 실행이 거부돼야 함")
	}
}
