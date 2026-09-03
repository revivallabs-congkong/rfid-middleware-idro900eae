package gui

import (
	"bytes"
	"strings"
	"testing"
)

// FR-20/G7 — 송출 라인에 토큰 전문이 절대 남지 않는다.
// EPC(태그값)는 운영 스탭 확인용으로 마스킹하지 않는다 (사용자 요청 2026-09-03).
func TestMaskToken(t *testing.T) {
	token := strings.Repeat("ab12", 16) // 64 hex
	line := []byte(`{"event":"X","token":"` + token + `","epc":"E2801170000002155EDD7076"}`)
	out := maskLogLine(line)
	if bytes.Contains(out, []byte(token)) {
		t.Fatal("토큰 전문이 마스킹되지 않음")
	}
	if !bytes.Contains(out, []byte(token[:8])) {
		t.Fatal("토큰 fingerprint 프리픽스가 없음")
	}
	// EPC 는 전체가 그대로 보여야 한다.
	if !bytes.Contains(out, []byte("E2801170000002155EDD7076")) {
		t.Fatalf("EPC 전체값이 보존되지 않음: %s", out)
	}
}
