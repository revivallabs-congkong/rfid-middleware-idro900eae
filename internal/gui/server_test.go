package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Meta{Version: "test", Mode: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.ln.Close() })
	time.Sleep(50 * time.Millisecond)
	return s
}

// G13 — nonce 없는 경로·타 Origin POST 거부.
func TestAccessControl(t *testing.T) {
	s := startTestServer(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", s.meta.Port)

	// nonce 없는 경로 → 404
	if res, err := http.Get(base + "/api/state"); err != nil || res.StatusCode != 404 {
		t.Fatalf("nonce 없는 경로가 거부되지 않음: %v %v", res.StatusCode, err)
	}
	// 올바른 nonce → 200
	if res, err := http.Get(s.URL() + "api/state"); err != nil || res.StatusCode != 200 {
		t.Fatalf("정상 요청 실패: %v %v", res, err)
	}
	// 타 Origin POST → 403
	req, _ := http.NewRequest("POST", s.URL()+"api/control/service", strings.NewReader(`{"action":"start","confirm":true}`))
	req.Header.Set("Origin", "http://evil.example")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 403 {
		t.Fatalf("타 Origin POST 가 허용됨: %d", res.StatusCode)
	}
	// confirm 없는 제어 → confirm_required
	req2, _ := http.NewRequest("POST", s.URL()+"api/control/service", strings.NewReader(`{"action":"start"}`))
	res2, _ := http.DefaultClient.Do(req2)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	json.NewDecoder(res2.Body).Decode(&body)
	if body.Error.Code != "confirm_required" {
		t.Fatalf("confirm 강제 실패: %+v", body)
	}
}

// FR-20 — /api/state 와 SSE state 에 64hex 토큰 형태가 없어야 한다 (구조상
// State 는 토큰을 담지 않지만, 회귀 방지로 직렬화 바이트를 검사).
func TestStateHasNoTokenShape(t *testing.T) {
	s := startTestServer(t)
	s.PushState(State{Mode: "observer", Signal: "green", Headline: "정상"})
	res, err := http.Get(s.URL() + "api/state")
	if err != nil {
		t.Fatal(err)
	}
	var buf [8192]byte
	n, _ := res.Body.Read(buf[:])
	if tokenLike.Match(buf[:n]) {
		t.Fatal("state 응답에 토큰 형태 문자열 존재")
	}
}
