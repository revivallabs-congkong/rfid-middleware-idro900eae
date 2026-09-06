package sender

import (
	"context"
	"math/rand"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// meta 재조회 주기 — 60초 ± 10초 지터 (pulse-middleware-v2 FR-02).
const (
	metaRefreshInterval = 60 * time.Second
	metaRefreshJitter   = 10 * time.Second
)

// MetaRefresher 는 활성(ACTIVE/ACTIVE_WARNING) 게이트의 meta 를 주기적으로 재조회해
// 서버 세션 전환(unitName·cooldownSec 변경)을 리더 카드·status.json 에 반영한다.
//
// 원칙 (FR-02·02a·02b):
//   - 활성 리더만 재조회한다. 그 외 상태는 서버를 호출하지 않는다.
//   - 200 + 계약 준수 body 만 반영한다. 전송 오류·4xx·5xx·body 위반은 무시하고
//     상태·fingerprint 를 건드리지 않는다. 재조회 중 404 는 회수 신호로 쓰지 않는다.
//   - 값이 바뀐 경우에만 영속화 + META_REFRESHED 로그 1건. 변화 없으면 쓰기·로그 없음.
//   - rebind 가드를 절대 타지 않는다: fingerprint 는 기존 값을 그대로 보존하고
//     meta 만 갱신한다.
//   - cooldownSec 0↔양수 변화 시 ACTIVE↔ACTIVE_WARNING 을 전환한다(멱등성 경고가
//     세션을 따라가야 함).
type MetaRefresher struct {
	Client *congkong.Client
	Gates  *gate.Registry
	Store  gate.Persister
	Clock  clock.Clock
	Log    *logging.Logger
	Rand   *rand.Rand // 테스트 주입용(nil 이면 시간 기반)
}

func (m *MetaRefresher) Run(ctx context.Context, readerID string, token domain.Secret) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.Clock.After(m.nextDelay()):
		}
		if ctx.Err() != nil {
			return
		}
		m.tick(ctx, readerID, token)
	}
}

// nextDelay 는 60초 ± 10초 지터다.
func (m *MetaRefresher) nextDelay() time.Duration {
	span := int64(2*metaRefreshJitter + 1)
	var n int64
	if m.Rand != nil {
		n = m.Rand.Int63n(span)
	} else {
		n = rand.Int63n(span)
	}
	return metaRefreshInterval + time.Duration(n) - metaRefreshJitter
}

// tick 은 1회 재조회다. 활성 리더가 아니면 서버를 호출하지 않는다.
func (m *MetaRefresher) tick(ctx context.Context, readerID string, token domain.Secret) {
	e, ok := m.Gates.Get(readerID)
	if !ok || !e.State.Sendable() {
		return // 활성(ACTIVE/ACTIVE_WARNING) 리더만 재조회
	}
	res := m.Client.Preflight(ctx, token)
	// 200 + 계약 준수만 반영. 그 외는 상태·fingerprint 불변.
	if res.TransportErr != nil || res.BodyErr != nil || res.Status != 200 {
		return
	}
	meta, valid := congkong.PreflightMeta(res.Body)
	if !valid {
		return
	}
	if meta == e.Meta {
		return // 변화 없음 — 영속화·로그 없음
	}
	// cooldownSec 0↔양수 → ACTIVE↔ACTIVE_WARNING (FR-02a, preflight 와 동일 문구)
	state := domain.GateActive
	reason := ""
	if meta.CooldownSec == 0 {
		state = domain.GateActiveWarning
		reason = "cooldownSec=0 — 재시도 멱등성 미보장"
	}
	// fingerprint 는 기존 값을 그대로 보존한다 (FR-02b — rebind 키 불변).
	if err := m.Gates.Set(m.Store, readerID, state, reason, m.Clock.Now().UnixMilli(), e.Fingerprint, &meta); err != nil {
		m.Log.Warnf("META_REFRESH_PERSIST_FAILED", logging.F{"readerId": readerID, "message": err.Error()})
		return
	}
	m.Log.Infof("META_REFRESHED", logging.F{
		"readerId":        readerID,
		"prevUnitName":    e.Meta.UnitName,
		"unitName":        meta.UnitName,
		"prevCooldownSec": e.Meta.CooldownSec,
		"cooldownSec":     meta.CooldownSec,
	})
}
