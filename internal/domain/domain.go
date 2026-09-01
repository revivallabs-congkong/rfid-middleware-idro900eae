// Package domain 은 미들웨어의 핵심 타입과 결정 값을 정의한다.
// OS·DB·네트워크를 import 하지 않는다 (설계서 §4 의존 방향).
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// TagRead 는 리더(또는 재생 입력)에서 수신한 태그 1건이다.
type TagRead struct {
	ReaderID   string
	EPC        string
	ReceivedAt time.Time
	RawLine    string // reader raw 로그 전용. DB 큐에는 저장하지 않는다.
}

// QueueItem 은 영속 큐의 한 행이다. checkedAt 은 최초 생성한 RFC3339Nano
// 문자열 그대로이며 재시도·재생에서 절대 바뀌지 않는다 (R4).
type QueueItem struct {
	ID              int64
	ReaderID        string
	EPC             string
	CheckedAt       string
	CheckedAtUnixMS int64
	AttemptCount    int
	NextAttemptAtMS int64
}

// DeliveryDecision 은 서버 응답 분류 결과다 (설계서 §8.4).
type DeliveryDecision int

const (
	DecisionComplete DeliveryDecision = iota
	DecisionDrop
	DecisionRetry
	DecisionSuspendReader
	DecisionHaltGlobal
)

func (d DeliveryDecision) String() string {
	switch d {
	case DecisionComplete:
		return "COMPLETE"
	case DecisionDrop:
		return "DROP"
	case DecisionRetry:
		return "RETRY"
	case DecisionSuspendReader:
		return "SUSPEND_READER"
	case DecisionHaltGlobal:
		return "HALT_GLOBAL"
	}
	return "UNKNOWN"
}

// GateState 는 reader 별 송신 상태다 (설계서 §8.5).
type GateState string

const (
	GatePreflightPending GateState = "PREFLIGHT_PENDING"
	GatePreflightRetry   GateState = "PREFLIGHT_RETRY"
	GateActive           GateState = "ACTIVE"
	GateActiveWarning    GateState = "ACTIVE_WARNING" // cooldownSec == 0, 송신은 허용 + 반복 경고
	GateSuspendedToken   GateState = "SUSPENDED_TOKEN"
	GateSuspendedConfig  GateState = "SUSPENDED_CONFIG"
	GateSuspendedRebind  GateState = "SUSPENDED_REBIND"
)

// Sendable 은 sender 가 이 reader 의 큐 행을 선택해도 되는 상태인지다.
func (g GateState) Sendable() bool {
	return g == GateActive || g == GateActiveWarning
}

// Suspended 는 새 TagRead 를 큐에 적재하지 않는 상태인지다.
// PREFLIGHT_PENDING/RETRY 는 적재한다 — 기동 직후 스캔을 잃지 않기 위해서다.
func (g GateState) Suspended() bool {
	return g == GateSuspendedToken || g == GateSuspendedConfig || g == GateSuspendedRebind
}

// SenderState 는 프로세스 전역 송신 상태다.
type SenderState string

const (
	SenderRunning SenderState = "RUNNING"
	// SenderHalted 는 400 error bind — 공통 요청 인코더의 자기 버그.
	// 큐 적재는 계속하되 HTTP POST 는 0건이어야 한다 (불변식 11).
	SenderHalted SenderState = "HALTED_REQUEST_BUG"
)

// GateMeta 는 preflight GET 의 최소 DTO 다. attendee 등 다른 필드는
// 선언하지 않는다 — Go decoder 가 미선언 필드를 버린다.
type GateMeta struct {
	EventName   string `json:"eventName"`
	BoothName   string `json:"boothName"`
	UnitName    string `json:"unitName"`
	CooldownSec int    `json:"cooldownSec"`
}

// Secret 은 펄스 토큰이다. 로그·JSON 어디에도 원문이 나가지 않는다.
// %v, %s, JSON marshal 은 전부 [REDACTED] 가 된다.
type Secret struct{ v string }

func NewSecret(v string) Secret { return Secret{v: v} }

func (s Secret) String() string   { return "[REDACTED]" }
func (s Secret) GoString() string { return "[REDACTED]" }

func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// Raw 는 HTTP path 조립 전용이다. 로그 경로에서 호출 금지.
func (s Secret) Raw() string { return s.v }

// Fingerprint 는 SHA-256 앞 12바이트 hex — 진단·rebind 감지용으로만 저장한다.
func (s Secret) Fingerprint() string {
	if s.v == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s.v))
	return hex.EncodeToString(h[:12])
}

func (s Secret) IsZero() bool { return s.v == "" }
