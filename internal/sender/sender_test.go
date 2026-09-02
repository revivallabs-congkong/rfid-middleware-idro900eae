package sender

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/store/sqlite"
)

var t0 = time.Date(2026, 9, 1, 10, 0, 0, 0, time.FixedZone("KST", 9*3600))

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type creds map[string]domain.Secret

func (c creds) Token(id string) (domain.Secret, bool) {
	s, ok := c[id]
	return s, ok
}

// fakeServer 는 응답 시나리오를 순서대로 재생하고 요청을 기록한다.
type fakeServer struct {
	mu        sync.Mutex
	responses []response
	requests  []request
	srv       *httptest.Server
}

type response struct {
	status int
	body   string
}

type request struct {
	path      string
	epc       string
	checkedAt string
}

func newFakeServer(t *testing.T, responses ...response) *fakeServer {
	f := &fakeServer{responses: responses}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BarcodeString string `json:"barcodeString"`
			CheckedAt     string `json:"checkedAt"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, request{path: r.URL.Path, epc: body.BarcodeString, checkedAt: body.CheckedAt})
		var res response
		if len(f.responses) > 0 {
			res = f.responses[0]
			f.responses = f.responses[1:]
		} else {
			res = response{200, `{"result":"success"}`}
		}
		f.mu.Unlock()
		w.WriteHeader(res.status)
		w.Write([]byte(res.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) reqs() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]request, len(f.requests))
	copy(out, f.requests)
	return out
}

func newSender(t *testing.T, apiHost string, clk clock.Clock) (*Sender, *sqlite.Store, *gate.Registry) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	client, err := congkong.New(apiHost, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gates := gate.NewRegistry()
	gates.Set(nil, "gate-a", domain.GateActive, "", t0.UnixMilli(), "", nil)
	log, _ := logging.New("", logging.Error, nil)
	return New(st, client, creds{"gate-a": domain.NewSecret(testToken)}, gates, clk, log, 24*time.Hour), st, gates
}

func enqueue(t *testing.T, st *sqlite.Store, reader, epc string, at time.Time) *domain.QueueItem {
	t.Helper()
	res, err := st.Admit(reader, epc, at, at.Format(time.RFC3339Nano), 60*time.Second)
	if err != nil || !res.Accepted {
		t.Fatalf("enqueue 실패: %+v %v", res, err)
	}
	it, err := st.NextDue(at.UnixMilli(), []string{reader})
	if err != nil || it == nil {
		t.Fatalf("NextDue 실패: %v", err)
	}
	return it
}

func TestDeliverSuccess(t *testing.T) {
	f := newFakeServer(t, response{200, `{"result":"success","attendee":{"fullName":"홍길동"}}`})
	s, st, _ := newSender(t, f.srv.URL, clock.Real{})
	it := enqueue(t, st, "gate-a", "ABCD", t0)
	s.deliver(context.Background(), it, t0.Add(time.Second))
	if d, _ := st.Depth(); d != 0 {
		t.Errorf("성공 후 큐가 비어야 함: %d", d)
	}
	reqs := f.reqs()
	if len(reqs) != 1 || reqs[0].epc != "ABCD" {
		t.Fatalf("reqs = %+v", reqs)
	}
	if reqs[0].checkedAt != t0.Format(time.RFC3339Nano) {
		t.Error("원래 checkedAt 이 전송돼야 함")
	}
	if !strings.HasSuffix(reqs[0].path, "/check-in") || !strings.Contains(reqs[0].path, testToken) {
		t.Errorf("path = %s", reqs[0].path)
	}
}

func Test409IsSuccess(t *testing.T) {
	f := newFakeServer(t, response{409, `{"message":"dup","code":409,"data":{"result":"success:duplication","checkedAt":"2026-01-01T00:00:00Z"}}`})
	s, st, _ := newSender(t, f.srv.URL, clock.Real{})
	it := enqueue(t, st, "gate-a", "ABCD", t0)
	s.deliver(context.Background(), it, t0.Add(time.Second))
	if d, _ := st.Depth(); d != 0 {
		t.Error("409 는 성공과 동일하게 완료")
	}
}

// B4: 미등록 404 뒤 다음 스캔이 계속 전송된다
func TestAcceptanceB4NotFoundThenContinue(t *testing.T) {
	f := newFakeServer(t,
		response{404, `{"message":"cannot find barcode","code":404,"data":{"result":"fail:barcode-not-found"}}`},
		response{200, `{"result":"success"}`},
	)
	s, st, gates := newSender(t, f.srv.URL, clock.Real{})
	it1 := enqueue(t, st, "gate-a", "DEAD", t0)
	s.deliver(context.Background(), it1, t0.Add(time.Second))

	// 미등록 태그는 gate 를 막지 않는다
	if g, _ := gates.Get("gate-a"); g.State != domain.GateActive {
		t.Fatalf("gate 상태 = %s, ACTIVE 여야 함", g.State)
	}
	it2 := enqueue(t, st, "gate-a", "BEEF", t0.Add(time.Second))
	s.deliver(context.Background(), it2, t0.Add(time.Second))
	if len(f.reqs()) != 2 {
		t.Errorf("다음 스캔이 전송돼야 함: %d", len(f.reqs()))
	}
	if d, _ := st.Depth(); d != 0 {
		t.Error("404 barcode 는 재큐 금지")
	}
}

// B5: 토큰 회수 404 → 해당 reader 송신 중단, 다른 reader 는 계속
func TestAcceptanceB5TokenRevokedSuspends(t *testing.T) {
	f := newFakeServer(t, response{404, `{"message":"resource not found","code":404,"InnerError":null,"data":null}`})
	s, st, gates := newSender(t, f.srv.URL, clock.Real{})
	it := enqueue(t, st, "gate-a", "ABCD", t0)
	s.deliver(context.Background(), it, t0.Add(time.Second))

	g, _ := gates.Get("gate-a")
	if g.State != domain.GateSuspendedToken {
		t.Fatalf("gate = %s, SUSPENDED_TOKEN 이어야 함", g.State)
	}
	if d, _ := st.Depth(); d != 0 {
		t.Error("회수 404 를 받은 현재 행은 삭제 (재큐 금지)")
	}
	// suspended reader 는 sendable 에서 빠져 이후 요청이 0건
	if len(gates.SendableReaders()) != 0 {
		t.Error("suspended reader 가 sendable 에 남음")
	}
	// gate 영속화 확인
	rows, _ := st.Gates()
	if rows["gate-a"].State != domain.GateSuspendedToken {
		t.Error("suspension 이 영속화돼야 함")
	}
}

// 5xx → 보존 + 같은 checkedAt 재시도 (B3 의 핵심)
func TestRetryKeepsOriginalCheckedAt(t *testing.T) {
	f := newFakeServer(t,
		response{500, `{}`},
		response{200, `{"result":"success"}`},
	)
	s, st, _ := newSender(t, f.srv.URL, clock.Real{})
	it := enqueue(t, st, "gate-a", "ABCD", t0)
	now := t0.Add(time.Second) // 결정론 — 실제 시각과 무관하게 만료 판정 회피
	s.deliver(context.Background(), it, now)

	if d, _ := st.Depth(); d != 1 {
		t.Fatal("5xx 는 보존")
	}
	// backoff 5초 후 due
	it2, _ := st.NextDue(now.Add(5*time.Second).UnixMilli(), []string{"gate-a"})
	if it2 == nil || it2.AttemptCount != 1 {
		t.Fatalf("retry 행: %+v", it2)
	}
	s.deliver(context.Background(), it2, now.Add(5*time.Second))
	reqs := f.reqs()
	if len(reqs) != 2 {
		t.Fatalf("2회 요청이어야 함: %d", len(reqs))
	}
	if reqs[0].checkedAt != reqs[1].checkedAt || reqs[1].checkedAt != t0.Format(time.RFC3339Nano) {
		t.Error("재시도에서 checkedAt 이 바뀜 (R4 위반)")
	}
	if d, _ := st.Depth(); d != 0 {
		t.Error("성공 후 완료돼야 함")
	}
}

func TestBackoffSchedule(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 5 * time.Second}, {2, 30 * time.Second}, {3, 2 * time.Minute}, {10, 2 * time.Minute},
	}
	for _, c := range cases {
		if got := backoffDelay(c.attempts); got != c.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", c.attempts, got, c.want)
		}
	}
}

// error bind → 전역 halt + 트리거 행 보존 (문서 검토 반영 사항)
func TestErrorBindHaltsGloballyAndKeepsRow(t *testing.T) {
	f := newFakeServer(t, response{400, `{"message":"error bind","code":400,"data":null}`})
	s, st, gates := newSender(t, f.srv.URL, clock.Real{})
	it := enqueue(t, st, "gate-a", "ABCD", t0)
	s.deliver(context.Background(), it, t0.Add(time.Second))

	if gates.GlobalState() != domain.SenderHalted {
		t.Fatal("전역 halt 여야 함")
	}
	if d, _ := st.Depth(); d != 1 {
		t.Error("트리거 행은 보존돼야 함 — 스캔은 유효하고 결함은 인코더에 있음")
	}
	// halt 중 Run 루프는 POST 를 보내지 않는다 (불변식 11)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if len(f.reqs()) != 1 {
		t.Errorf("halt 중 추가 POST 발생: %d", len(f.reqs()))
	}
}

// 24시간 초과 행은 HTTP 호출 없이 만료된다 (R6)
func TestExpiredItemNotSent(t *testing.T) {
	f := newFakeServer(t)
	s, st, _ := newSender(t, f.srv.URL, clock.Real{})
	now := t0.Add(time.Second)
	old := now.Add(-25 * time.Hour) // 만료 임계(24h) 초과
	it := enqueue(t, st, "gate-a", "ABCD", old)
	s.deliver(context.Background(), it, now)
	if len(f.reqs()) != 0 {
		t.Error("만료 행은 서버에 보내지 않는다")
	}
	if d, _ := st.Depth(); d != 0 {
		t.Error("만료 행은 삭제")
	}
}

// 전체 루프: enqueue → 자동 전송 (Wake 신호)
func TestRunLoopDrainsQueue(t *testing.T) {
	f := newFakeServer(t)
	s, st, _ := newSender(t, f.srv.URL, clock.Real{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	base := time.Now()
	for i, epc := range []string{"AAAA", "BBBB", "CCCC"} {
		if _, err := st.Admit("gate-a", epc, base.Add(time.Duration(i)*time.Millisecond),
			base.Format(time.RFC3339Nano), 60*time.Second); err != nil {
			t.Fatal(err)
		}
		s.Wake()
	}
	deadline := time.After(5 * time.Second)
	for {
		if d, _ := st.Depth(); d == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("큐가 드레인되지 않음")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if len(f.reqs()) != 3 {
		t.Errorf("요청 %d건, want 3", len(f.reqs()))
	}
}

// --- preflight ---

func TestPreflightStates(t *testing.T) {
	cases := []struct {
		name  string
		res   response
		state domain.GateState
	}{
		{"active", response{200, `{"eventName":"E","boothName":"A 게이트","unitName":"U","action":"in","cooldownSec":60}`}, domain.GateActive},
		{"cooldown-zero", response{200, `{"eventName":"E","boothName":"A 게이트","unitName":"U","cooldownSec":0}`}, domain.GateActiveWarning},
		{"revoked", response{404, `{"message":"resource not found","code":404,"data":null}`}, domain.GateSuspendedToken},
		{"invalid-200", response{200, `{}`}, domain.GateSuspendedConfig},
		{"weird-4xx", response{403, `{}`}, domain.GateSuspendedConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.res.status)
				w.Write([]byte(c.res.body))
			}))
			defer srv.Close()
			client, _ := congkong.New(srv.URL, time.Second)
			gates := gate.NewRegistry()
			log, _ := logging.New("", logging.Error, nil)
			p := &Preflight{Client: client, Gates: gates, Clock: clock.Real{}, Log: log}
			p.Run(context.Background(), "gate-a", domain.NewSecret(testToken))
			if g, _ := gates.Get("gate-a"); g.State != c.state {
				t.Errorf("state = %s, want %s", g.State, c.state)
			}
		})
	}
}

// ACTIVE_WARNING 은 송신을 허용한다 (검토 반영: cooldown=0 이어도 송신 유지)
func TestActiveWarningIsSendable(t *testing.T) {
	if !domain.GateActiveWarning.Sendable() {
		t.Error("ACTIVE_WARNING 은 sendable 이어야 함")
	}
}

// 토큰 변경 + gate 이름 불일치 → SUSPENDED_REBIND
func TestPreflightRebindDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"B 게이트","unitName":"U","cooldownSec":60}`))
	}))
	defer srv.Close()
	client, _ := congkong.New(srv.URL, time.Second)
	gates := gate.NewRegistry()
	// 이전 토큰의 gate 상태 복원 시나리오
	gates.Init("gate-a", gate.Entry{
		State: domain.GateActive, Fingerprint: "old-fingerprint",
		Meta: domain.GateMeta{EventName: "E", BoothName: "A 게이트", UnitName: "U", CooldownSec: 60},
	})
	log, _ := logging.New("", logging.Error, nil)
	p := &Preflight{Client: client, Gates: gates, Clock: clock.Real{}, Log: log}
	p.Run(context.Background(), "gate-a", domain.NewSecret(testToken))
	if g, _ := gates.Get("gate-a"); g.State != domain.GateSuspendedRebind {
		t.Errorf("state = %s, SUSPENDED_REBIND 여야 함", g.State)
	}
}

// 같은 이름 tuple 이면 토큰이 바뀌어도 ACTIVE 로 진행
func TestPreflightSameGateNewTokenProceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"U","cooldownSec":60}`))
	}))
	defer srv.Close()
	client, _ := congkong.New(srv.URL, time.Second)
	gates := gate.NewRegistry()
	gates.Init("gate-a", gate.Entry{
		State: domain.GateActive, Fingerprint: "old-fingerprint",
		Meta: domain.GateMeta{EventName: "E", BoothName: "A 게이트", UnitName: "U", CooldownSec: 60},
	})
	log, _ := logging.New("", logging.Error, nil)
	p := &Preflight{Client: client, Gates: gates, Clock: clock.Real{}, Log: log}
	p.Run(context.Background(), "gate-a", domain.NewSecret(testToken))
	if g, _ := gates.Get("gate-a"); g.State != domain.GateActive {
		t.Errorf("state = %s, ACTIVE 여야 함", g.State)
	}
}
