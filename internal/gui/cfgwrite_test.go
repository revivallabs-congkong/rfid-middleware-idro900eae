package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
)

func writeCfg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{"version":1,"apiHost":"https://api.congkong.net","dataDir":"` +
		strings.ReplaceAll(dir, `\`, `\\`) + `","readers":[
	  {"id":"gate-a","addr":"192.168.9.6:5578","pulseToken":"` + tok1 + `"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteReaderSessionAndRollback(t *testing.T) {
	p := writeCfg(t)
	orig, _ := os.ReadFile(p)

	bak, err := WriteReaderSession(p, "gate-a", "s2", tok2)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("쓰기 후 로드 실패: %v", err)
	}
	r, _ := cfg.Reader("gate-a")
	if r.Token.Raw() != tok2 || r.SessionID != "s2" {
		t.Fatalf("반영 실패: sessionId=%s", r.SessionID)
	}

	// 롤백 → 원본 복원
	if err := RollbackConfig(p, bak); err != nil {
		t.Fatal(err)
	}
	now, _ := os.ReadFile(p)
	if string(now) != string(orig) {
		t.Fatal("롤백이 원본을 복원하지 못함")
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatal("롤백 후 .bak 이 남음")
	}
}

func TestWriteReaderSessionUnknownReader(t *testing.T) {
	p := writeCfg(t)
	if _, err := WriteReaderSession(p, "nope", "s", tok2); err == nil {
		t.Fatal("없는 리더 거부 실패")
	}
}
