package gui

import (
	"bytes"
	"strings"
	"testing"
)

// FR-20/G7 — 송출 라인에 토큰 전문이 절대 남지 않는다.
func TestMaskTokenAndEPC(t *testing.T) {
	token := strings.Repeat("ab12", 16) // 64 hex
	line := []byte(`{"event":"X","token":"` + token + `","epc":"E2801170000002155EDD7076"}`)
	out := maskLogLine(line)
	if bytes.Contains(out, []byte(token)) {
		t.Fatal("토큰 전문이 마스킹되지 않음")
	}
	if !bytes.Contains(out, []byte(token[:8])) {
		t.Fatal("토큰 fingerprint 프리픽스가 없음")
	}
	if bytes.Contains(out, []byte("EDD7076")) || !bytes.Contains(out, []byte(`E2801170000002155EDD****`)) {
		t.Fatalf("EPC 끝 4자 마스킹 실패: %s", out)
	}
}
