package sender

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// countPersister 는 SetGate 호출 횟수와 마지막 fingerprint 를 기록한다.
type countPersister struct {
	n       int
	lastFP  string
	lastHas bool
}

func (c *countPersister) SetGate(readerID string, state domain.GateState, reason string, nowMS int64, fp string, meta *domain.GateMeta) error {
	c.n++
	c.lastFP = fp
	c.lastHas = true
	return nil
}

func newRefresher(srvURL string, gates *gate.Registry, store gate.Persister) *MetaRefresher {
	client, _ := congkong.New(srvURL, time.Second)
	log, _ := logging.New("", logging.Error, nil)
	return &MetaRefresher{Client: client, Gates: gates, Store: store, Clock: clock.Real{}, Log: log}
}

func activeGate(gates *gate.Registry, unit string, cd int) {
	gates.Init("gate-a", gate.Entry{
		State: domain.GateActive, Fingerprint: "fp1",
		Meta: domain.GateMeta{EventName: "E", BoothName: "A 게이트", UnitName: unit, CooldownSec: cd},
	})
}

// 값이 바뀌면 meta 갱신 + fingerprint 보존 + 영속화 1회.
func TestMetaRefreshUpdatesOnChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"세션2","cooldownSec":60}`))
	}))
	defer srv.Close()
	gates := gate.NewRegistry()
	activeGate(gates, "세션1", 60)
	cp := &countPersister{}
	newRefresher(srv.URL, gates, cp).tick(context.Background(), "gate-a", domain.NewSecret(testToken))

	g, _ := gates.Get("gate-a")
	if g.Meta.UnitName != "세션2" {
		t.Errorf("unitName = %q, want 세션2", g.Meta.UnitName)
	}
	if g.State != domain.GateActive {
		t.Errorf("state = %s, want ACTIVE", g.State)
	}
	if g.Fingerprint != "fp1" {
		t.Errorf("fingerprint = %q, 재조회가 fingerprint 를 바꿨음 (fp1 이어야 함)", g.Fingerprint)
	}
	if cp.n != 1 {
		t.Errorf("SetGate 호출 %d회, 1회여야 함", cp.n)
	}
	if cp.lastFP != "fp1" {
		t.Errorf("영속화 fingerprint = %q, 기존 fp1 을 보존해야 함", cp.lastFP)
	}
}

// 변화 없으면 영속화·로그 없음 (SQLite 쓰기 0건).
func TestMetaRefreshNoChangeNoWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"세션1","cooldownSec":60}`))
	}))
	defer srv.Close()
	gates := gate.NewRegistry()
	activeGate(gates, "세션1", 60)
	cp := &countPersister{}
	newRefresher(srv.URL, gates, cp).tick(context.Background(), "gate-a", domain.NewSecret(testToken))
	if cp.n != 0 {
		t.Errorf("SetGate 호출 %d회, 변화 없으면 0회여야 함", cp.n)
	}
}

// 5xx/404/body 위반 → 상태·fingerprint 불변, 영속화 0회.
func TestMetaRefreshIgnoresErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"5xx", 500, `{}`},
		{"404", 404, `{"message":"not found"}`},
		{"invalid-body", 200, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			}))
			defer srv.Close()
			gates := gate.NewRegistry()
			activeGate(gates, "세션1", 60)
			cp := &countPersister{}
			newRefresher(srv.URL, gates, cp).tick(context.Background(), "gate-a", domain.NewSecret(testToken))
			g, _ := gates.Get("gate-a")
			if g.State != domain.GateActive || g.Meta.UnitName != "세션1" || g.Fingerprint != "fp1" {
				t.Errorf("오류 응답이 상태를 바꿈: state=%s unit=%q fp=%q", g.State, g.Meta.UnitName, g.Fingerprint)
			}
			if cp.n != 0 {
				t.Errorf("오류 응답에도 SetGate %d회 (0회여야 함)", cp.n)
			}
		})
	}
}

// cooldownSec 0↔양수 → ACTIVE↔ACTIVE_WARNING 전환 (FR-02a).
func TestMetaRefreshCooldownTransition(t *testing.T) {
	// 60 → 0 : ACTIVE → ACTIVE_WARNING
	srv0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"세션1","cooldownSec":0}`))
	}))
	defer srv0.Close()
	gates := gate.NewRegistry()
	activeGate(gates, "세션1", 60)
	newRefresher(srv0.URL, gates, &countPersister{}).tick(context.Background(), "gate-a", domain.NewSecret(testToken))
	if g, _ := gates.Get("gate-a"); g.State != domain.GateActiveWarning {
		t.Errorf("cooldown 0 후 state = %s, ACTIVE_WARNING 여야 함", g.State)
	}
	// 0 → 60 : ACTIVE_WARNING → ACTIVE
	srv60 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"세션1","cooldownSec":60}`))
	}))
	defer srv60.Close()
	newRefresher(srv60.URL, gates, &countPersister{}).tick(context.Background(), "gate-a", domain.NewSecret(testToken))
	if g, _ := gates.Get("gate-a"); g.State != domain.GateActive {
		t.Errorf("cooldown 60 후 state = %s, ACTIVE 여야 함", g.State)
	}
}

// 비활성(활성 아님) 리더는 서버를 호출하지 않는다.
func TestMetaRefreshSkipsInactive(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"eventName":"E","boothName":"A 게이트","unitName":"세션2","cooldownSec":60}`))
	}))
	defer srv.Close()
	gates := gate.NewRegistry()
	gates.Init("gate-a", gate.Entry{State: domain.GateSuspendedToken, Fingerprint: "fp1"})
	cp := &countPersister{}
	newRefresher(srv.URL, gates, cp).tick(context.Background(), "gate-a", domain.NewSecret(testToken))
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("비활성 리더가 서버를 %d회 호출함 (0회여야 함)", hits)
	}
	if cp.n != 0 {
		t.Errorf("비활성 리더 SetGate %d회 (0회여야 함)", cp.n)
	}
}

// 재조회 주기는 60초 ± 10초 안에 있다.
func TestMetaRefreshNextDelayBounds(t *testing.T) {
	m := &MetaRefresher{}
	for i := 0; i < 1000; i++ {
		d := m.nextDelay()
		if d < metaRefreshInterval-metaRefreshJitter || d > metaRefreshInterval+metaRefreshJitter {
			t.Fatalf("nextDelay=%s, [%s,%s] 범위 밖", d, metaRefreshInterval-metaRefreshJitter, metaRefreshInterval+metaRefreshJitter)
		}
	}
}
