package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// G8/FR-20 — 리포트에 토큰·EPC 전문이 절대 남지 않는다.
func TestWizardReportRedaction(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("ab12", 16) // 64 hex
	st := WizardState{
		StartedAt: "2026-09-02T10:00:00+09:00", FinishedAt: "2026-09-02T10:01:00+09:00",
		Steps: []StepResult{
			{ID: "2", Title: "서버 확인", Status: StepPass, Detail: "토큰 " + token + " 확인",
				Metrics: map[string]string{"epc": "E2801170000002155EDD7076", "token": token}},
		},
	}
	path, err := SaveReport(dir, "v-test", "cfg1234", st)
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"", ".json"} {
		p := path
		if ext == ".json" {
			p = strings.TrimSuffix(path, ".txt") + ".json"
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), token) {
			t.Fatalf("%s 에 토큰 전문 유출", p)
		}
		if strings.Contains(string(b), "EDD7076") {
			t.Fatalf("%s 에 EPC 전문 유출", p)
		}
	}
}

// logging.Redact 단위: 토큰·EPC·금칙키.
func TestRedactHelper(t *testing.T) {
	token := strings.Repeat("cd34", 16)
	out := logging.Redact(logging.F{
		"note": "값 " + token + " 포함", "epc": "AABBCCDDEEFF001122334455",
		"pulseToken": token, "readerId": "gate-a",
	})
	if _, ok := out["pulseToken"]; ok {
		t.Fatal("금칙키 pulseToken 이 남음")
	}
	if strings.Contains(out["note"].(string), token) {
		t.Fatal("note 의 토큰 미마스킹")
	}
	if epc := out["epc"].(string); !strings.HasSuffix(epc, "****") || strings.Contains(epc, "4455") {
		t.Fatalf("epc 마스킹 실패: %v", epc)
	}
	if out["readerId"] != "gate-a" {
		t.Fatal("일반 필드 손상")
	}
}

// 단계 0: 카탈로그 없음 → 경고(실패 아님), 정상 config 는 통과 경로.
func TestWizardStep0(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dd := strings.ReplaceAll(dir, `\`, `\\`)
	os.WriteFile(cfgPath, []byte(`{"version":1,"apiHost":"http://127.0.0.1:1","dataDir":"`+dd+`","readers":[{"id":"gate-a","addr":"192.168.9.6:5578","pulseToken":"`+tok1+`"}]}`), 0o600)
	w := NewWizard(wizardEnv{
		cfgPath:     cfgPath,
		catalogPath: func() string { return filepath.Join(dir, "pulse-sessions.json") },
		dataDir:     func() string { return dir },
		coreRunning: func() bool { return false },
		readerConn:  func(string) string { return "" },
	}, nil)
	cfg, err := loadCfgForWizard(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	res := w.step0(cfg, nil)
	// 카탈로그 파일 없음 → 경고
	if res.Status != StepWarn {
		t.Fatalf("step0 = %s (%s), 경고 기대", res.Status, res.Detail)
	}
}
