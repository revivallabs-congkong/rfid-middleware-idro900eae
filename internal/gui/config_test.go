package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
)

// 설정 UI 저장: 토큰 보존 + 전역 필드·addr 반영 + 신규 리더 placeholder.
func TestWriteConfigPreservesTokensAndValidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	dd := strings.ReplaceAll(dir, `\`, `\\`)
	os.WriteFile(p, []byte(`{"version":1,"apiHost":"https://api.congkong.net","dataDir":"`+dd+`",
	  "powerGain":300,"readers":[{"id":"gate-a","addr":"192.168.9.6:5578","pulseToken":"`+tok1+`","sessionId":"s1"}]}`), 0o600)

	e := ConfigEdit{
		APIHost: "https://api.congkong.net", DataDir: dir, DebounceSec: 30,
		QueueMaxAgeHours: 24, RequestTimeoutSec: 10, PowerGain: 200, Buzzer: 1, LogLevel: "debug",
		Readers: []ReaderEdit{
			{ID: "gate-a", Addr: "192.168.9.10:5578"}, // addr 변경, 토큰 보존 기대
			{ID: "gate-b", Addr: "192.168.9.11:5578"}, // 신규 — placeholder 토큰
		},
	}
	if _, err := WriteConfig(p, e); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("저장 후 로드 실패: %v", err)
	}
	if cfg.PowerGain != 200 || cfg.DebounceSec != 30 || cfg.Buzzer != 1 || cfg.LogLevel != "debug" {
		t.Fatalf("전역 필드 미반영: %+v", cfg)
	}
	ga, _ := cfg.Reader("gate-a")
	if ga.Token.Raw() != tok1 || ga.SessionID != "s1" || ga.Addr != "192.168.9.10:5578" {
		t.Fatalf("gate-a 토큰/세션 보존 또는 addr 실패: addr=%s sid=%s", ga.Addr, ga.SessionID)
	}
	gb, ok := cfg.Reader("gate-b")
	if !ok || gb.Addr != "192.168.9.11:5578" {
		t.Fatalf("신규 리더 gate-b 실패")
	}
}

func TestWriteConfigCreatesNew(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new", "config.json")
	e := ConfigEdit{
		APIHost: "https://api.congkong.net", DataDir: dir, DebounceSec: 60,
		QueueMaxAgeHours: 24, RequestTimeoutSec: 10, PowerGain: 300, Buzzer: 0, LogLevel: "info",
		Readers: []ReaderEdit{{ID: "gate-a", Addr: "192.168.9.6:5578"}},
	}
	if _, err := WriteConfig(p, e); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err != nil {
		t.Fatalf("신규 설정 로드 실패: %v", err)
	}
}
