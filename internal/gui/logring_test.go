package gui

import (
	"testing"
	"time"
)

// GUI 설계 §6.3 — Write 는 소비자가 없어도 절대 블록하지 않는다.
func TestLogRingNonBlockingAndDropOldest(t *testing.T) {
	r := NewLogRing(3)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.Write([]byte{byte('a' + i%26)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Write 가 블록됨")
	}
	if got := len(r.Drain()); got != 3 {
		t.Fatalf("drop-oldest 실패: %d행 남음", got)
	}
	if r.Dropped() != 97 {
		t.Fatalf("dropped=%d", r.Dropped())
	}
}
