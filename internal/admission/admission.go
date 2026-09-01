// Package admission 은 EPC 이벤트 수락 use case 다 (설계서 §3.1, §7.3).
// durable debounce + enqueue 를 store 트랜잭션에 위임하고 결과를 로그한다.
package admission

import (
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/store/sqlite"
)

type Service struct {
	Store    *sqlite.Store
	Gates    *gate.Registry
	Log      *logging.Logger
	Debounce time.Duration
	// Wake 는 새 enqueue 를 sender 에 알린다.
	Wake func()
}

// Handle 은 파싱된 TagRead 1건을 처리한다. 어떤 실패도 panic 하지 않는다.
func (s *Service) Handle(read domain.TagRead) {
	// suspended token 에는 새 queue 행을 만들지 않는다 (불변식 10).
	if s.Gates.Suspended(read.ReaderID) {
		s.Log.Warnf("SCAN_DROPPED_SUSPENDED", logging.F{
			"readerId": read.ReaderID, "epc": read.EPC,
		})
		return
	}

	// checkedAt 은 수신 시각을 RFC3339Nano 로 한 번 포맷한 문자열이며
	// 모든 재시도에서 그대로 사용한다 (R4, 설계서 §7.3).
	checkedAt := read.ReceivedAt.Format(time.RFC3339Nano)

	res, err := s.Store.Admit(read.ReaderID, read.EPC, read.ReceivedAt, checkedAt, s.Debounce)
	if err != nil {
		// DB commit 실패 — 성공 처리하지 않고 치명 ERROR (설계서 §7.3).
		s.Log.Errorf("ENQUEUE_FAILED", logging.F{
			"readerId": read.ReaderID, "epc": read.EPC, "message": err.Error(),
		})
		return
	}
	if res.ClockBackward {
		s.Log.Warnf("CLOCK_MOVED_BACKWARD", logging.F{
			"readerId": read.ReaderID, "epc": read.EPC,
		})
	}
	if !res.Accepted {
		s.Log.Debugf("DEBOUNCE_DROP", logging.F{
			"readerId": read.ReaderID, "epc": read.EPC,
		})
		return
	}
	s.Log.Infof("SCAN_ENQUEUED", logging.F{
		"readerId": read.ReaderID, "epc": read.EPC, "checkedAt": checkedAt, "itemId": res.ItemID,
	})
	if s.Wake != nil {
		s.Wake()
	}
}
