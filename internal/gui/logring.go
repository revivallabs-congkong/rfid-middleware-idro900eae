package gui

import (
	"sync"
	"sync/atomic"
)

// LogRing 은 코어 Echo 에 꽂는 non-blocking writer 다 (GUI 설계 §6.3).
// logging.Log 가 mutex 안에서 Echo 를 동기 호출하므로, Write 는 복사 후 즉시
// 반환하고 별도 고루틴이 SSE 로 flush 한다 — 느린 UI 가 파이프라인을 절대
// 블록하지 않는다. 가득 차면 가장 오래된 행부터 버린다 (drop-oldest).
type LogRing struct {
	mu      sync.Mutex
	buf     [][]byte
	cap     int
	notify  chan struct{}
	dropped atomic.Int64
}

func NewLogRing(capacity int) *LogRing {
	if capacity <= 0 {
		capacity = 4096
	}
	return &LogRing{cap: capacity, notify: make(chan struct{}, 1)}
}

// Write 는 io.Writer 구현이다. 절대 블록하지 않는다.
func (r *LogRing) Write(p []byte) (int, error) {
	line := make([]byte, len(p))
	copy(line, p)
	r.mu.Lock()
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
		r.dropped.Add(1)
	}
	r.buf = append(r.buf, line)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

// Drain 은 쌓인 행을 모두 꺼낸다 (flush 고루틴 전용).
func (r *LogRing) Drain() [][]byte {
	r.mu.Lock()
	out := r.buf
	r.buf = nil
	r.mu.Unlock()
	return out
}

func (r *LogRing) Notify() <-chan struct{} { return r.notify }
func (r *LogRing) Dropped() int64          { return r.dropped.Load() }
