// Package sender 는 전역 1개의 순차 전송 워커다 (ADR-004, 설계서 §8.5~8.6).
// 실패 행은 미래 next_attempt_at 으로 미뤄 다른 due 행을 막지 않는다.
package sender

import (
	"context"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/store/sqlite"
)

// maxIdleWait — clock 변화에 대비해 타이머는 최대 30초 단위로 재평가한다 (설계서 §8.6).
const maxIdleWait = 30 * time.Second

// CredentialProvider 는 readerId → 현재 설정 토큰이다 (계획서 §5.2).
type CredentialProvider interface {
	Token(readerID string) (domain.Secret, bool)
}

type Sender struct {
	Store  *sqlite.Store
	Client *congkong.Client
	Creds  CredentialProvider
	Gates  *gate.Registry
	Clock  clock.Clock
	Log    *logging.Logger
	MaxAge time.Duration // queueMaxAgeHours
	// OnSuccess 는 체크인 확정(200/409 Complete) 관측 콜백이다 (선택 —
	// lastSuccessAt·successSinceStart 기록용, GUI 설계 §6.1).
	OnSuccess func(readerID string)

	wake chan struct{}
}

func New(store *sqlite.Store, client *congkong.Client, creds CredentialProvider,
	gates *gate.Registry, clk clock.Clock, log *logging.Logger, maxAge time.Duration) *Sender {
	return &Sender{
		Store: store, Client: client, Creds: creds, Gates: gates,
		Clock: clk, Log: log, MaxAge: maxAge,
		wake: make(chan struct{}, 1),
	}
}

// Wake 는 새 enqueue/상태 변경을 알린다.
func (s *Sender) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// backoffDelay 는 실패한 HTTP 시도 수 기준이다: 1→5s, 2→30s, 3+→2m (프로토콜 §6).
func backoffDelay(failedAttempts int) time.Duration {
	switch {
	case failedAttempts <= 1:
		return 5 * time.Second
	case failedAttempts == 2:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}

// Run 은 ctx 취소까지 순차 전송을 반복한다. in-flight POST 는 최대 1건이다 (불변식 12).
func (s *Sender) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		// global halt 중에는 HTTP POST 0건 (불변식 11).
		if s.Gates.GlobalState() == domain.SenderHalted {
			if !s.waitSignal(ctx, maxIdleWait) {
				return
			}
			continue
		}

		now := s.Clock.Now()
		item, err := s.Store.NextDue(now.UnixMilli(), s.Gates.SendableReaders())
		if err != nil {
			s.Log.Errorf("QUEUE_READ_FAILED", logging.F{"message": err.Error()})
			if !s.waitSignal(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if item == nil {
			if !s.waitSignal(ctx, s.idleWait(now)) {
				return
			}
			continue
		}
		s.deliver(ctx, item, now)
	}
}

// idleWait 는 다음 due 까지의 대기 시간이다 (상한 30초).
func (s *Sender) idleWait(now time.Time) time.Duration {
	dueMS, ok, err := s.Store.NextDueAtMS(s.Gates.SendableReaders())
	if err != nil || !ok {
		return maxIdleWait
	}
	d := time.Duration(dueMS-now.UnixMilli()) * time.Millisecond
	if d < 0 {
		d = 0
	}
	if d > maxIdleWait {
		d = maxIdleWait
	}
	return d
}

// waitSignal 은 wake/gate 변경/타이머 중 먼저 오는 것을 기다린다.
// ctx 취소면 false 를 돌려준다.
func (s *Sender) waitSignal(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
	case <-s.Gates.Changed():
	case <-s.Clock.After(d):
	}
	return true
}

func (s *Sender) deliver(ctx context.Context, item *domain.QueueItem, now time.Time) {
	// 전송 직전 24시간 만료 검사 — HTTP 호출 없이 삭제 (설계서 §7.4).
	if item.CheckedAtUnixMS < now.Add(-s.MaxAge).UnixMilli() {
		if err := s.Store.Complete(item.ID); err == nil {
			s.Log.Warnf("QUEUE_ITEM_EXPIRED", logging.F{
				"readerId": item.ReaderID, "epc": item.EPC, "checkedAt": item.CheckedAt,
			})
		}
		return
	}

	token, ok := s.Creds.Token(item.ReaderID)
	if !ok {
		// 설정에서 사라진 reader — 송신 불가. gate 를 막고 보존한다.
		s.Gates.Set(s.Store, item.ReaderID, domain.GateSuspendedConfig, "reader 가 설정에 없음",
			now.UnixMilli(), "", nil)
		s.Log.Errorf("READER_TOKEN_MISSING", logging.F{"readerId": item.ReaderID})
		return
	}

	res := s.Client.CheckIn(ctx, token, item.EPC, item.CheckedAt)
	decision, class := congkong.Classify(res)

	fields := logging.F{
		"readerId": item.ReaderID, "epc": item.EPC, "checkedAt": item.CheckedAt,
		"httpStatus": res.Status, "resultClass": class, "decision": decision.String(),
		"attempt": item.AttemptCount + 1,
	}

	switch decision {
	case domain.DecisionComplete:
		if err := s.Store.Complete(item.ID); err != nil {
			s.Log.Errorf("QUEUE_ACK_FAILED", logging.F{"itemId": item.ID, "message": err.Error()})
			return
		}
		s.Log.Infof("SEND_RESULT", fields)
		if s.OnSuccess != nil {
			s.OnSuccess(item.ReaderID)
		}

	case domain.DecisionDrop:
		if err := s.Store.Complete(item.ID); err != nil {
			s.Log.Errorf("QUEUE_ACK_FAILED", logging.F{"itemId": item.ID, "message": err.Error()})
			return
		}
		level := logging.Warn
		if class == congkong.ClassBarcodeNotFound {
			level = logging.Info // 미등록 태그는 정상 운영 이벤트 — 서버가 자동 집계
		}
		if class == congkong.ClassCheckedAtFormat || class == congkong.ClassCheckedAtRange {
			fields["message"] = "시스템 시계/NTP 를 점검하세요"
			level = logging.Error
		}
		if class == congkong.ClassEmptyUIDBug {
			fields["message"] = "리더/파서 점검 필요 — 빈 UID 전송"
			level = logging.Error
		}
		s.Log.Log(level, "SEND_RESULT", fields)

	case domain.DecisionRetry:
		delay := backoffDelay(item.AttemptCount + 1)
		next := now.Add(delay).UnixMilli()
		if err := s.Store.RetryLater(item.ID, next, class, res.Status); err != nil {
			s.Log.Errorf("QUEUE_RETRY_FAILED", logging.F{"itemId": item.ID, "message": err.Error()})
			return
		}
		fields["retryInSec"] = int(delay.Seconds())
		if res.TransportErr != nil {
			fields["message"] = res.TransportErr.Error()
		}
		s.Log.Warnf("SEND_RESULT", fields)

	case domain.DecisionSuspendReader:
		// 토큰 회수 — 현재 행은 SSOT 대로 삭제(재큐 금지), 해당 reader 만 중단 (R9, ADR-005).
		if err := s.Store.Complete(item.ID); err != nil {
			s.Log.Errorf("QUEUE_ACK_FAILED", logging.F{"itemId": item.ID, "message": err.Error()})
		}
		s.Gates.Set(s.Store, item.ReaderID, domain.GateSuspendedToken,
			"404 — 토큰 회수/무효", now.UnixMilli(), "", nil)
		s.Log.Errorf("TOKEN_SUSPENDED", fields)

	case domain.DecisionHaltGlobal:
		// 자기 요청 버그 — 전역 송신 중단. 트리거 행은 보존한다 (설계서 §8.5).
		if err := s.Store.MarkAttemptKeepDue(item.ID, class, res.Status); err != nil {
			s.Log.Errorf("QUEUE_MARK_FAILED", logging.F{"itemId": item.ID, "message": err.Error()})
		}
		s.Gates.HaltGlobal()
		s.Log.Errorf("SENDER_HALTED_REQUEST_BUG", fields)
	}
}
