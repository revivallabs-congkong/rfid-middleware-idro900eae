package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

// §3.4 오케스트레이션: 중복 세션 거부, pending 차단, 정상 적용(관측 모드).
func newTestApp(t *testing.T) *guiApp {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dd := strings.ReplaceAll(dir, `\`, `\\`)
	body := `{"version":1,"apiHost":"https://api.congkong.net","dataDir":"` + dd + `","readers":[
	  {"id":"gate-a","addr":"192.168.9.6:5578","pulseToken":"` + tok1 + `","sessionId":"s1"},
	  {"id":"gate-b","addr":"192.168.9.7:5578","pulseToken":"` + tok2 + `"}]}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catPath := filepath.Join(dir, "pulse-sessions.json")
	tok3 := "3670000000000000000000000000000000000000000000000000000000000003"
	cat := `{"version":1,"eventName":"E","exportedAt":"2026-09-02T10:00:00+09:00","sessions":[
	  {"id":"s1","name":"세션1","unitName":"U1","pulseToken":"` + tok1 + `"},
	  {"id":"s3","name":"세션3","unitName":"U3","pulseToken":"` + tok3 + `"}]}`
	if err := os.WriteFile(catPath, []byte(cat), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ga := &guiApp{cfgPath: cfgPath, mode: "observer", cfg: cfg, dataDir: cfg.DataDir,
		mgr: &CatalogManager{Path: catPath}}
	ga.mgr.reload(false)
	return ga
}

func TestApplySessionDuplicateRejected(t *testing.T) {
	ga := newTestApp(t)
	// s1 은 gate-a 에 이미 지정 — gate-b 에 지정 시도 → 거부 (config 중복 토큰 정책)
	if _, e := ga.applySession("gate-b", "s1"); e == nil || e.Code != "conflict_busy" {
		t.Fatalf("중복 거부 실패: %+v", e)
	}
}

func TestApplySessionPendingBlocked(t *testing.T) {
	ga := newTestApp(t)
	// status.json 에 pending 을 만들어 차단 확인 (§3.4)
	health.WriteStatus(filepath.Join(ga.dataDir, "status.json"), health.Status{
		UpdatedAt: "2026-09-02T10:00:00+09:00", SenderState: "RUNNING",
		Readers: []health.ReaderStatus{{ID: "gate-b", Pending: 3}},
	})
	if _, e := ga.applySession("gate-b", "s3"); e == nil || e.Code != "conflict_busy" ||
		!strings.Contains(e.Message, "3건") {
		t.Fatalf("pending 차단 실패: %+v", e)
	}
}

func TestApplySessionObserverOK(t *testing.T) {
	ga := newTestApp(t)
	res, e := ga.applySession("gate-b", "s3")
	if e != nil {
		t.Fatalf("적용 실패: %+v", e)
	}
	if r, ok := res.(applyResult); !ok || !r.NeedsServiceRestart {
		t.Fatalf("관측 모드 결과: %+v", res)
	}
	cfg, _ := config.Load(ga.cfgPath)
	r, _ := cfg.Reader("gate-b")
	if r.SessionID != "s3" {
		t.Fatalf("config 미반영: %+v", r)
	}
	// 적용 성공 후 .bak 는 없어야 한다
	if _, err := os.Stat(ga.cfgPath + ".bak"); !os.IsNotExist(err) {
		t.Fatal(".bak 잔존")
	}
}
