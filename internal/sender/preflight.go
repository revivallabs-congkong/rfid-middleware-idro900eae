package sender

import (
	"context"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// Preflight 는 reader 별 송신 시작 전 토큰 자기 검증이다 (설계서 §8.2, B1).
// 확정 상태에 도달할 때까지 5s→30s→2m backoff 로 재시도한다.
type Preflight struct {
	Client *congkong.Client
	Gates  *gate.Registry
	Store  gate.Persister
	Clock  clock.Clock
	Log    *logging.Logger
}

func (p *Preflight) Run(ctx context.Context, readerID string, token domain.Secret) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		done := p.attempt(ctx, readerID, token)
		if done {
			return
		}
		attempt++
		select {
		case <-ctx.Done():
			return
		case <-p.Clock.After(backoffDelay(attempt)):
		}
	}
}

// attempt 는 확정 상태에 도달하면 true 를 돌려준다.
func (p *Preflight) attempt(ctx context.Context, readerID string, token domain.Secret) bool {
	nowMS := p.Clock.Now().UnixMilli()
	res := p.Client.Preflight(ctx, token)
	fp := token.Fingerprint()

	// NTP 보조 진단 — Date 헤더와 로컬 시각 차이 (설계서 §12.1).
	if res.HasDateSkew {
		skew := res.DateSkew
		if skew < 0 {
			skew = -skew
		}
		switch {
		case skew >= 4*time.Minute:
			p.Log.Errorf("CLOCK_SKEW", logging.F{"readerId": readerID, "skewSec": int(res.DateSkew.Seconds()),
				"message": "서버와 4분 이상 차이 — checkedAt 이 400 으로 폐기될 수 있음, NTP 동기화 필요"})
		case skew >= time.Minute:
			p.Log.Warnf("CLOCK_SKEW", logging.F{"readerId": readerID, "skewSec": int(res.DateSkew.Seconds())})
		}
	}

	switch {
	case res.TransportErr != nil || res.BodyErr != nil || res.Status >= 500:
		p.Gates.Set(p.Store, readerID, domain.GatePreflightRetry, "네트워크/서버 오류", nowMS, fp, nil)
		p.Log.Warnf("PREFLIGHT_RETRY", logging.F{"readerId": readerID, "httpStatus": res.Status})
		return false

	case res.Status == 200:
		meta, ok := congkong.PreflightMeta(res.Body)
		if !ok {
			p.Gates.Set(p.Store, readerID, domain.GateSuspendedConfig, "preflight 200 body 계약 위반", nowMS, fp, nil)
			p.Log.Errorf("PREFLIGHT_INVALID_BODY", logging.F{"readerId": readerID})
			return true
		}
		// 토큰이 바뀌면서 gate 이름 tuple 도 바뀌면 pending 오배송 가능성 —
		// 자동 송신하지 않고 운영자 resume 을 요구한다 (설계서 §8.2).
		if prev, exists := p.Gates.Get(readerID); exists &&
			prev.Fingerprint != "" && prev.Fingerprint != fp &&
			prev.Meta.BoothName != "" && prev.Meta != meta {
			p.Gates.Set(p.Store, readerID, domain.GateSuspendedRebind,
				"토큰 변경 + gate 불일치 — queue resume 필요", nowMS, fp, &meta)
			p.Log.Errorf("GATE_SUSPENDED_REBIND", logging.F{
				"readerId": readerID, "boothName": meta.BoothName,
				"message": "rfid-middleware queue resume --reader " + readerID + " --pending send|discard 로 재개",
			})
			return true
		}
		// PII 없이 boothName 을 로그하고 cooldownSec 을 확인한다 (B1).
		if meta.CooldownSec > 0 {
			p.Gates.Set(p.Store, readerID, domain.GateActive, "", nowMS, fp, &meta)
			p.Log.Infof("PREFLIGHT_OK", logging.F{
				"readerId": readerID, "eventName": meta.EventName,
				"boothName": meta.BoothName, "unitName": meta.UnitName, "cooldownSec": meta.CooldownSec,
			})
		} else {
			// cooldown 0 — 멱등성 미보장. 송신은 허용하되 반복 경고 (ACTIVE_WARNING).
			p.Gates.Set(p.Store, readerID, domain.GateActiveWarning,
				"cooldownSec=0 — 재시도 멱등성 미보장", nowMS, fp, &meta)
			p.Log.Errorf("PREFLIGHT_COOLDOWN_ZERO", logging.F{
				"readerId": readerID, "boothName": meta.BoothName,
				"message": "운영진에게 유닛 쿨다운 설정을 요청하세요 — 0 이면 응답 유실 재시도 시 중복이 쌓입니다",
			})
		}
		return true

	case res.Status == 404:
		p.Gates.Set(p.Store, readerID, domain.GateSuspendedToken, "preflight 404 — 토큰 회수/무효", nowMS, fp, nil)
		p.Log.Errorf("TOKEN_SUSPENDED", logging.F{"readerId": readerID, "httpStatus": 404})
		return true

	default:
		p.Gates.Set(p.Store, readerID, domain.GateSuspendedConfig, "preflight 예상 밖 응답", nowMS, fp, nil)
		p.Log.Errorf("PREFLIGHT_UNEXPECTED", logging.F{"readerId": readerID, "httpStatus": res.Status})
		return true
	}
}
