// Package clock 은 실제/가짜 시계를 제공한다. 시간 의존 코드는 전부 이
// 인터페이스를 주입받아 sleep 없이 테스트한다 (계획서 §9.1).
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	// After 는 d 뒤에 신호가 오는 채널을 돌려준다.
	After(d time.Duration) <-chan time.Time
}

// Real 은 시스템 시계다.
type Real struct{}

func (Real) Now() time.Time                         { return time.Now() }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake 는 테스트용 시계다. Advance 로만 시간이 흐른다.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- f.now
		return t.ch
	}
	f.timers = append(f.timers, t)
	return t.ch
}

// Advance 는 시간을 d 만큼 진행시키고 도래한 타이머를 깨운다.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now
	var rest []*fakeTimer
	var fire []*fakeTimer
	for _, t := range f.timers {
		if !t.at.After(now) {
			fire = append(fire, t)
		} else {
			rest = append(rest, t)
		}
	}
	f.timers = rest
	f.mu.Unlock()
	for _, t := range fire {
		t.ch <- now
	}
}
