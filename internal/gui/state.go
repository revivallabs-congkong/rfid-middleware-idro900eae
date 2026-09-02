// Package gui 는 트레이 + 로컬 웹 UI 다 (GUI 설계서). M1: 관측 모드 —
// status.json 폴링과 로그 tail 로 서비스/코어를 읽기 전용으로 보여준다.
package gui

import (
	"fmt"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

// State 는 /api/state 응답이다 (GUI 설계 §4.3). 토큰 전문은 절대 담지 않는다.
type State struct {
	Mode              string               `json:"mode"`
	Signal            string               `json:"signal"` // green|yellow|red|gray
	Headline          string               `json:"headline"`
	SenderState       string               `json:"senderState"`
	QueueDepth        int64                `json:"queueDepth"`
	OldestCheckedAt   string               `json:"oldestCheckedAt,omitempty"`
	NTP               *health.NTPInfo      `json:"ntp,omitempty"`
	UpdatedAt         string               `json:"updatedAt"`
	CoreRunning       bool                 `json:"coreRunning"`
	CoreVersion       string               `json:"coreVersion,omitempty"`
	CoreMode          string               `json:"coreMode,omitempty"`
	Collecting        bool                 `json:"collecting"`        // 수집 중(코어 실행 + 전역 halt 아님)
	Standby           bool                 `json:"standby"`           // 수집 대기(운영자가 아직 켜지 않음)
	NeedsSession      bool                 `json:"needsSession"`      // 유효한 세션(토큰) 미지정 — 수집 시작 불가
	Network           string               `json:"network"`           // online | offline | unknown
	NetworkText       string               `json:"networkText"`
	SuccessSinceStart int64                `json:"successSinceStart"`
	LogDropped        int64                `json:"logDropped,omitempty"`
	Catalog           *CatalogState        `json:"catalog,omitempty"`
	Readers           []ReaderView         `json:"readers"`
}

// CatalogState 는 State 안의 카탈로그 요약이다 (GUI 설계 §4.3).
type CatalogState struct {
	Loaded          bool   `json:"loaded"`
	EventName       string `json:"eventName,omitempty"`
	ExportedAt      string `json:"exportedAt,omitempty"`
	Stale           bool   `json:"stale,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
	PendingImport   bool   `json:"pendingImport,omitempty"`
	Error           string `json:"error,omitempty"`
}

type ReaderView struct {
	ID              string `json:"id"`
	SessionID       string `json:"sessionId,omitempty"`
	SessionName     string `json:"sessionName,omitempty"`
	SessionVerified bool   `json:"sessionVerified,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
	NeedsSession    bool   `json:"needsSession,omitempty"`
	ConnState     string `json:"connState"`
	ConnSince     string `json:"connSince,omitempty"`
	GateState     string `json:"gateState"`
	GateText      string `json:"gateText"`
	ActionText    string `json:"actionText,omitempty"`
	BoothName     string `json:"boothName,omitempty"`
	UnitName      string `json:"unitName,omitempty"`
	CooldownSec   int    `json:"cooldownSec,omitempty"`
	Pending       int64  `json:"pending"`
	LastTagAt     string `json:"lastTagAt,omitempty"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
}

// gateTexts 는 내부 상태 → 표시/조치 문장 번역표다 (GUI 설계 §2.3).
var gateTexts = map[string][2]string{
	string(domain.GatePreflightPending): {"서버 확인 대기", "잠시 후 자동 진행됩니다"},
	string(domain.GatePreflightRetry):   {"서버 연결 재시도 중", "인터넷 연결을 확인하세요"},
	string(domain.GateActive):           {"정상", ""},
	string(domain.GateActiveWarning):    {"정상 (중복 방지 꺼짐)", "운영진에게 쿨다운 설정을 요청하세요"},
	string(domain.GateSuspendedToken):   {"토큰 회수됨 — 중단", "새 카탈로그 파일을 받아 재개하세요"},
	string(domain.GateSuspendedConfig):  {"서버 응답 이상 — 중단", "개발팀에 연락하세요 (서버 계약 위반)"},
	string(domain.GateSuspendedRebind):  {"세션이 바뀜 — 확인 필요", "대기 중 기록의 전송/폐기를 선택해 재개하세요"},
}

const connActionText = "리더 전원·케이블을 확인하세요. 다른 프로그램(YAT 등)이 리더에 접속해 있으면 종료 후 리더 전원을 재투입하세요"

// BuildState 는 status.json(v2) 스냅샷에서 화면 상태를 계산한다.
// statusErr 는 파일 없음/파싱 실패이고, mode 는 GUI 자신의 모드다.
func BuildState(s health.Status, statusErr error, mode string, now time.Time, queueMaxAgeHours int) State {
	out := State{Mode: mode, SenderState: s.SenderState}

	out.Network, out.NetworkText = "unknown", "인터넷 연결 확인 중"

	if statusErr != nil {
		out.Signal = "gray"
		out.Collecting = false
		out.Headline = "수집 중지됨 — 서비스를 시작하거나 설정을 확인하세요"
		return out
	}
	updated, uerr := time.Parse(time.RFC3339, s.UpdatedAt)
	if uerr != nil || now.Sub(updated) > 15*time.Second {
		out.Signal = "red"
		out.Collecting = false
		out.Headline = "서비스가 응답하지 않음 — 서비스 상태를 확인하세요"
		out.UpdatedAt = s.UpdatedAt
		return out
	}

	// 수집 중 = 코어가 살아 응답 중 + 전역 송신 중단 아님
	out.Collecting = s.SenderState == string(domain.SenderRunning)
	if s.Server != nil && s.Server.Seen {
		if s.Server.Online {
			out.Network, out.NetworkText = "online", "인터넷 연결됨"
		} else {
			out.Network, out.NetworkText = "offline", "인터넷 연결 끊김 — 큐에 보관 중"
		}
	}

	out.CoreRunning = true
	out.QueueDepth = s.QueueDepth
	out.OldestCheckedAt = s.OldestCheckedAt
	out.NTP = s.NTP
	out.UpdatedAt = s.UpdatedAt
	out.CoreVersion = s.Version
	out.CoreMode = s.Mode
	out.SuccessSinceStart = s.SuccessSinceStart

	for _, r := range s.Readers {
		v := ReaderView{
			ID: r.ID, SessionID: r.SessionID,
			ConnState: r.ConnState, ConnSince: r.ConnSince,
			GateState: r.GateState,
			BoothName: r.BoothName, UnitName: r.UnitName,
			CooldownSec: r.CooldownSec, Pending: r.Pending,
			LastTagAt: r.LastTagAt, LastSuccessAt: r.LastSuccessAt,
		}
		if t, ok := gateTexts[r.GateState]; ok {
			v.GateText, v.ActionText = t[0], t[1]
		} else {
			v.GateText = r.GateState
		}
		if r.ConnState == "DISCONNECTED" {
			if disconnectedOver(r.ConnSince, now, 30*time.Second) {
				v.GateText = "리더 연결 끊김"
				v.ActionText = connActionText
			} else if v.ActionText == "" {
				v.ActionText = "리더 재접속 중 — 잠시 기다리세요"
			}
		}
		out.Readers = append(out.Readers, v)
	}

	out.Signal, out.Headline = evalSignal(s, now, queueMaxAgeHours)
	return out
}

func disconnectedOver(since string, now time.Time, d time.Duration) bool {
	if since == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, since)
	return err != nil || now.Sub(t) > d
}

// evalSignal 은 §2.2 신호등 규칙(우선순위 순, 첫 일치)이다.
func evalSignal(s health.Status, now time.Time, maxAgeHours int) (string, string) {
	// 1. 전역 중단
	if s.SenderState != string(domain.SenderRunning) {
		return "red", "전송이 전역 중단됨 — 프로그램 결함, 개발팀 연락 필요. 스캔은 계속 저장되는 중"
	}
	// 2·3. suspension
	for _, r := range s.Readers {
		if r.GateState == string(domain.GateSuspendedToken) {
			return "red", fmt.Sprintf("'%s' 토큰이 회수됨 — 새 카탈로그 파일로 재개 필요", r.ID)
		}
	}
	for _, r := range s.Readers {
		if r.GateState == string(domain.GateSuspendedConfig) || r.GateState == string(domain.GateSuspendedRebind) {
			return "red", fmt.Sprintf("'%s' 전송 중단 — 카드의 조치 안내를 확인하세요", r.ID)
		}
	}
	// 4. 리더 끊김 30s 초과
	for _, r := range s.Readers {
		if r.ConnState == "DISCONNECTED" && disconnectedOver(r.ConnSince, now, 30*time.Second) {
			return "red", fmt.Sprintf("'%s' 리더 연결 끊김 — 전원·케이블 확인", r.ID)
		}
	}
	// 5. 시계 skew
	if s.NTP != nil && s.NTP.Checked {
		skew := s.NTP.SkewSec
		if skew < 0 {
			skew = -skew
		}
		if skew >= 240 {
			return "red", "PC 시계가 서버와 4분 이상 어긋남 — 체크인이 거부될 수 있음, 시간 동기화 필요"
		}
	}
	// 6. 만료 임박 (코어 경고와 같은 90% 기준)
	if s.OldestCheckedAt != "" && maxAgeHours > 0 {
		if t, err := time.Parse(time.RFC3339Nano, s.OldestCheckedAt); err == nil {
			if now.Sub(t) > time.Duration(maxAgeHours)*time.Hour*9/10 {
				return "red", "미전송 기록이 곧 만료됨 — 네트워크 복구 필요"
			}
		}
	}
	// 7. 재시도 중
	for _, r := range s.Readers {
		if r.GateState == string(domain.GatePreflightRetry) ||
			(r.ConnState == "DISCONNECTED" && !disconnectedOver(r.ConnSince, now, 30*time.Second)) {
			return "yellow", "서버/리더 연결 재시도 중"
		}
	}
	// 8. cooldown 꺼짐
	for _, r := range s.Readers {
		if r.GateState == string(domain.GateActiveWarning) {
			return "yellow", "중복 방지(쿨다운)가 꺼져 있음 — 운영진에 설정 요청"
		}
	}
	// 9. 큐 적체 5분
	if s.QueueDepth > 0 && s.QueueNonEmptySince != "" {
		if t, err := time.Parse(time.RFC3339, s.QueueNonEmptySince); err == nil && now.Sub(t) > 5*time.Minute {
			return "yellow", fmt.Sprintf("미전송 %d건 대기 중 — 인터넷 연결 확인", s.QueueDepth)
		}
	}
	// 10. NTP 미확인
	if s.NTP == nil || !s.NTP.Checked {
		return "yellow", "시계 동기화 상태 미확인 — 잠시 후 자동 확인됩니다"
	}
	// 11. 정상
	return "green", fmt.Sprintf("정상 운영 중 · 이번 실행 체크인 %d건", s.SuccessSinceStart)
}
