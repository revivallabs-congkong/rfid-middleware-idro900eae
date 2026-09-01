package app

import (
	"sync"
	"time"
)

// telemetry 는 status.json v2 의 관측 필드를 모으는 in-memory 집계자다
// (GUI 설계 §6.1). 콜백은 파이프라인 고루틴에서 불리므로 짧게 유지한다.
type telemetry struct {
	mu      sync.Mutex
	readers map[string]*readerTele

	successSinceStart  int64
	queueNonEmptySince time.Time

	ntpChecked bool
	skewSec    int
	skewAt     time.Time
}

type readerTele struct {
	lastTagAt     time.Time
	lastSuccessAt time.Time
	connected     bool
	connSince     time.Time
}

func newTelemetry(readerIDs []string, now time.Time) *telemetry {
	t := &telemetry{readers: make(map[string]*readerTele, len(readerIDs))}
	for _, id := range readerIDs {
		t.readers[id] = &readerTele{connSince: now}
	}
	return t
}

func (t *telemetry) tagSeen(id string, now time.Time) {
	t.mu.Lock()
	if r, ok := t.readers[id]; ok {
		r.lastTagAt = now
	}
	t.mu.Unlock()
}

func (t *telemetry) success(id string, now time.Time) {
	t.mu.Lock()
	if r, ok := t.readers[id]; ok {
		r.lastSuccessAt = now
	}
	t.successSinceStart++
	t.mu.Unlock()
}

func (t *telemetry) conn(id string, up bool, now time.Time) {
	t.mu.Lock()
	if r, ok := t.readers[id]; ok && r.connected != up {
		r.connected = up
		r.connSince = now
	}
	t.mu.Unlock()
}

func (t *telemetry) setSkew(skewSec int, now time.Time) {
	t.mu.Lock()
	t.ntpChecked = true
	t.skewSec = skewSec
	t.skewAt = now
	t.mu.Unlock()
}

// queueDepthObserved 는 status 기록 시점의 큐 깊이로 nonEmptySince 를 갱신하고
// 현재 값을 돌려준다.
func (t *telemetry) queueDepthObserved(depth int64, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if depth == 0 {
		t.queueNonEmptySince = time.Time{}
	} else if t.queueNonEmptySince.IsZero() {
		t.queueNonEmptySince = now
	}
	return t.queueNonEmptySince
}

type readerSnap struct {
	lastTagAt, lastSuccessAt time.Time
	connected                bool
	connSince                time.Time
}

func (t *telemetry) snapshot() (map[string]readerSnap, int64, bool, int, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]readerSnap, len(t.readers))
	for id, r := range t.readers {
		out[id] = readerSnap{r.lastTagAt, r.lastSuccessAt, r.connected, r.connSince}
	}
	return out, t.successSinceStart, t.ntpChecked, t.skewSec, t.skewAt
}
