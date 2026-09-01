package gui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

var now = time.Date(2026, 9, 2, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))

func base() health.Status {
	return health.Status{
		Schema:      2,
		UpdatedAt:   now.Add(-2 * time.Second).Format(time.RFC3339),
		SenderState: "RUNNING",
		NTP:         &health.NTPInfo{Checked: true, SkewSec: 1},
		Readers: []health.ReaderStatus{{
			ID: "gate-a", GateState: "ACTIVE", ConnState: "CONNECTED",
			ConnSince: now.Add(-time.Hour).Format(time.RFC3339),
		}},
	}
}

// G3 — §2.2 신호등 규칙 시나리오.
func TestSignalRules(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*health.Status)
		signal string
		want   string // headline 부분 일치
	}{
		{"정상", func(s *health.Status) {}, "green", "정상 운영 중"},
		{"전역 중단", func(s *health.Status) { s.SenderState = "HALTED_REQUEST_BUG" }, "red", "전역 중단"},
		{"토큰 회수", func(s *health.Status) { s.Readers[0].GateState = "SUSPENDED_TOKEN" }, "red", "토큰이 회수됨"},
		{"리더 끊김 30s 초과", func(s *health.Status) {
			s.Readers[0].ConnState = "DISCONNECTED"
			s.Readers[0].ConnSince = now.Add(-time.Minute).Format(time.RFC3339)
		}, "red", "리더 연결 끊김"},
		{"리더 끊김 30s 이내", func(s *health.Status) {
			s.Readers[0].ConnState = "DISCONNECTED"
			s.Readers[0].ConnSince = now.Add(-5 * time.Second).Format(time.RFC3339)
		}, "yellow", "재시도 중"},
		{"시계 skew", func(s *health.Status) { s.NTP.SkewSec = -300 }, "red", "시계"},
		{"만료 임박", func(s *health.Status) {
			s.OldestCheckedAt = now.Add(-23 * time.Hour).Format(time.RFC3339Nano)
		}, "red", "만료"},
		{"쿨다운 꺼짐", func(s *health.Status) { s.Readers[0].GateState = "ACTIVE_WARNING" }, "yellow", "쿨다운"},
		{"큐 적체 5분", func(s *health.Status) {
			s.QueueDepth = 3
			s.QueueNonEmptySince = now.Add(-6 * time.Minute).Format(time.RFC3339)
		}, "yellow", "미전송 3건"},
		{"NTP 미확인", func(s *health.Status) { s.NTP = nil }, "yellow", "미확인"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			c.mut(&s)
			st := BuildState(s, nil, "observer", now, 24)
			if st.Signal != c.signal || !strings.Contains(st.Headline, c.want) {
				t.Fatalf("signal=%s headline=%q, want %s / *%s*", st.Signal, st.Headline, c.signal, c.want)
			}
		})
	}
}

func TestStaleAndMissingStatus(t *testing.T) {
	s := base()
	s.UpdatedAt = now.Add(-30 * time.Second).Format(time.RFC3339)
	if st := BuildState(s, nil, "observer", now, 24); st.Signal != "red" || !strings.Contains(st.Headline, "응답하지 않음") {
		t.Fatalf("stale: %+v", st)
	}
	if st := BuildState(health.Status{}, errors.New("없음"), "observer", now, 24); st.Signal != "gray" {
		t.Fatalf("missing: %+v", st)
	}
}

func TestReaderActionText(t *testing.T) {
	s := base()
	s.Readers[0].GateState = "SUSPENDED_TOKEN"
	st := BuildState(s, nil, "observer", now, 24)
	r := st.Readers[0]
	if r.GateText != "토큰 회수됨 — 중단" || !strings.Contains(r.ActionText, "카탈로그") {
		t.Fatalf("번역표 불일치: %+v", r)
	}
}
