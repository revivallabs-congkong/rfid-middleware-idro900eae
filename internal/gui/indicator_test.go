package gui

import (
	"errors"
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

// 수집·인터넷 인디케이터 파생.
func TestCollectingAndNetwork(t *testing.T) {
	s := base()
	s.Server = &health.ServerInfo{Seen: true, Online: true}
	st := BuildState(s, nil, "observer", now, 24)
	if !st.Collecting || st.Network != "online" {
		t.Fatalf("정상: collecting=%v network=%s", st.Collecting, st.Network)
	}

	// 인터넷 끊김
	s.Server = &health.ServerInfo{Seen: true, Online: false}
	st = BuildState(s, nil, "observer", now, 24)
	if st.Network != "offline" {
		t.Fatalf("offline 기대: %s", st.Network)
	}

	// 서버 통신 이력 없음 → unknown
	s.Server = nil
	st = BuildState(s, nil, "observer", now, 24)
	if st.Network != "unknown" {
		t.Fatalf("unknown 기대: %s", st.Network)
	}

	// 전역 halt → 수집 중지
	s.SenderState = "HALTED_REQUEST_BUG"
	st = BuildState(s, nil, "observer", now, 24)
	if st.Collecting {
		t.Fatal("halt 시 collecting=false 여야 함")
	}

	// status 없음 → 수집 중지
	st = BuildState(health.Status{}, errors.New("없음"), "observer", now, 24)
	if st.Collecting || st.Signal != "gray" {
		t.Fatalf("미수집: collecting=%v signal=%s", st.Collecting, st.Signal)
	}
}
