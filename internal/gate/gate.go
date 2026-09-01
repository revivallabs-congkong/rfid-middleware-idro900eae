// Package gate 는 reader 별 gate 상태와 프로세스 전역 sender 상태의
// 메모리 레지스트리다. 변경은 store 에도 영속화한다 (설계서 §8.5).
package gate

import (
	"sync"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

// Persister 는 상태 변경 영속화 인터페이스다 (store/sqlite 가 구현).
type Persister interface {
	SetGate(readerID string, state domain.GateState, reason string, nowMS int64, fingerprint string, meta *domain.GateMeta) error
}

type Entry struct {
	State       domain.GateState
	Reason      string
	Fingerprint string
	Meta        domain.GateMeta
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
	global  domain.SenderState
	// changed 는 sender 전용 단일 소비자 채널이다 (버퍼 1). GUI 등 다른
	// 관측자는 Subscribe 를 써야 한다 — changed 를 읽으면 sender 깨우기
	// 신호를 훔친다 (GUI 설계 §6.6).
	changed chan struct{}
	subs    map[int]chan struct{}
	nextSub int
}

func NewRegistry() *Registry {
	return &Registry{
		entries: map[string]Entry{},
		global:  domain.SenderRunning,
		changed: make(chan struct{}, 1),
		subs:    map[int]chan struct{}{},
	}
}

// Changed 는 상태 변화 신호 채널이다 (sender 전용 — 위 주석 참조).
func (r *Registry) Changed() <-chan struct{} { return r.changed }

// Subscribe 는 다중 관측자용 coalesced 알림 채널을 돌려준다. cancel 을
// 호출하면 구독이 해제된다. sender 의 Changed 와 독립이다.
func (r *Registry) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

func (r *Registry) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
	r.mu.RLock()
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	r.mu.RUnlock()
}

// Init 은 기동 시 영속화된 상태를 복원한다 (persist 없이).
func (r *Registry) Init(readerID string, e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[readerID] = e
}

// Set 은 상태를 바꾸고 persister 에 영속화한다.
func (r *Registry) Set(p Persister, readerID string, state domain.GateState, reason string, nowMS int64, fingerprint string, meta *domain.GateMeta) error {
	r.mu.Lock()
	e := r.entries[readerID]
	e.State = state
	e.Reason = reason
	if fingerprint != "" {
		e.Fingerprint = fingerprint
	}
	if meta != nil {
		e.Meta = *meta
	}
	r.entries[readerID] = e
	r.mu.Unlock()
	r.notify()
	if p == nil {
		return nil
	}
	return p.SetGate(readerID, state, reason, nowMS, fingerprint, meta)
}

func (r *Registry) Get(readerID string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[readerID]
	return e, ok
}

// SendableReaders 는 sender 가 큐 행을 선택해도 되는 reader 목록이다.
func (r *Registry) SendableReaders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for id, e := range r.entries {
		if e.State.Sendable() {
			out = append(out, id)
		}
	}
	return out
}

// Suspended 는 새 TagRead 를 적재하면 안 되는 상태인지다 (불변식 10).
func (r *Registry) Suspended(readerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[readerID]
	return ok && e.State.Suspended()
}

func (r *Registry) GlobalState() domain.SenderState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.global
}

// HaltGlobal 은 400 error bind — 프로세스 전역 송신 중단이다 (불변식 11).
func (r *Registry) HaltGlobal() {
	r.mu.Lock()
	r.global = domain.SenderHalted
	r.mu.Unlock()
	r.notify()
}

// ResumeGlobal 은 새 바이너리/설정 검증 + 명시적 resume 후에만 호출한다.
func (r *Registry) ResumeGlobal() {
	r.mu.Lock()
	r.global = domain.SenderRunning
	r.mu.Unlock()
	r.notify()
}

// Snapshot 은 status.json 용 사본이다.
func (r *Registry) Snapshot() (map[string]Entry, domain.SenderState) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Entry, len(r.entries))
	for k, v := range r.entries {
		out[k] = v
	}
	return out, r.global
}
