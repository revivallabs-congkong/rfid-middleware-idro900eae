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

// 단일 리더 이름(ID) 변경 시 세션 토큰이 유실되지 않아야 한다.
func TestWriteConfigRenamePreservesToken(t *testing.T) {
	p := writeCfg(t) // gate-a + tok1
	dir := filepath.Dir(p)
	_, err := WriteConfig(p, ConfigEdit{
		APIHost: "https://api.congkong.net", DataDir: dir,
		DebounceSec: 60, QueueMaxAgeHours: 24, RequestTimeoutSec: 10,
		PowerGain: 300, Buzzer: 0, LogLevel: "info",
		Readers: []ReaderEdit{{ID: "gate-x", Addr: "192.168.9.6:5578"}}, // 이름만 변경
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cfg.Reader("gate-x")
	if !ok {
		t.Fatal("이름 변경된 리더가 없음")
	}
	if r.Token.Raw() != tok1 {
		t.Fatalf("이름 변경 후 토큰 유실: %s", r.Token.Raw())
	}
}

func TestWriteReaderSessionUnknownReader(t *testing.T) {
	p := writeCfg(t)
	if _, err := WriteReaderSession(p, "nope", "s", tok2); err == nil {
		t.Fatal("없는 리더 거부 실패")
	}
}
