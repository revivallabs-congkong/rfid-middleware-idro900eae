package gate

import (
	"testing"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

// GUI 설계 §6.6 — Subscribe 는 sender 의 Changed 신호를 훔치지 않는다.
func TestSubscribeDoesNotStealChanged(t *testing.T) {
	r := NewRegistry()
	sub, cancel := r.Subscribe()
	defer cancel()

	r.Set(nil, "gate-a", domain.GateActive, "", 0, "fp", nil)

	select {
	case <-r.Changed():
	default:
		t.Fatal("sender Changed 신호가 사라짐")
	}
	select {
	case <-sub:
	default:
		t.Fatal("Subscribe 구독자가 신호를 받지 못함")
	}
}

func TestSubscribeCancel(t *testing.T) {
	r := NewRegistry()
	_, cancel := r.Subscribe()
	cancel()
	r.mu.RLock()
	n := len(r.subs)
	r.mu.RUnlock()
	if n != 0 {
		t.Fatalf("구독 해제 후에도 %d개 남음", n)
	}
}
