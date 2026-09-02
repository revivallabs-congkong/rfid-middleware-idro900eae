package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

// 세션 카탈로그 pulse-sessions.json v1 (GUI 설계 §3.2). 관용 파싱 —
// 알 수 없는 필드는 무시한다 (congkong-v3 가 필드를 추가해도 안전).

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type CatalogSession struct {
	ID         string
	Name       string
	UnitName   string
	TokenLabel string
	Token      string // 전문 — Catalog 밖(API·로그·화면)으로 절대 내보내지 않는다
	TokenFP    string // 앞 8자 표시용
	IssuedAt   string
}

type Catalog struct {
	EventName  string
	ExportedAt string
	Sessions   []CatalogSession
	Warnings   []string // 항목 단위 제외 사유 (§3.6)
	LoadedAt   time.Time
}

// Stale 은 exportedAt 이 7일 이상 과거인지다 (§3.6 정보 배너).
func (c *Catalog) Stale(now time.Time) bool {
	t, err := time.Parse(time.RFC3339, c.ExportedAt)
	return err == nil && now.Sub(t) > 7*24*time.Hour
}

func (c *Catalog) Find(sessionID string) (CatalogSession, bool) {
	for _, s := range c.Sessions {
		if s.ID == sessionID {
			return s, true
		}
	}
	return CatalogSession{}, false
}

type catalogWire struct {
	Version  *int   `json:"version"`
	Event    string `json:"eventName"`
	Exported string `json:"exportedAt"`
	Sessions []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		UnitName   string `json:"unitName"`
		TokenLabel string `json:"tokenLabel"`
		PulseToken string `json:"pulseToken"`
		IssuedAt   string `json:"issuedAt"`
	} `json:"sessions"`
}

// LoadCatalog 는 파일을 읽어 §3.2 규칙대로 파싱한다.
func LoadCatalog(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseCatalog(b)
}

// ParseCatalog 는 바이트를 §3.2 규칙대로 파싱한다. version≠1 은 전체 거부,
// 항목 단위 오류(토큰 형식·id 중복)는 해당 항목만 제외하고 Warnings 에 남긴다.
func ParseCatalog(b []byte) (*Catalog, error) {
	var w catalogWire
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("카탈로그 파싱 실패: %w", err)
	}
	if w.Version == nil || *w.Version != 1 {
		return nil, fmt.Errorf("지원하지 않는 카탈로그 version — 프로그램 업데이트 또는 파일 재내보내기 필요")
	}
	if w.Event == "" || len(w.Sessions) == 0 {
		return nil, fmt.Errorf("eventName/sessions 가 비어 있음")
	}
	c := &Catalog{EventName: w.Event, ExportedAt: w.Exported, LoadedAt: time.Now()}
	seen := map[string]bool{}
	for _, s := range w.Sessions {
		switch {
		case s.ID == "" || s.Name == "" || s.UnitName == "":
			c.Warnings = append(c.Warnings, fmt.Sprintf("항목 %q: 필수 필드 누락 — 제외", s.ID))
		case !tokenPattern.MatchString(s.PulseToken):
			c.Warnings = append(c.Warnings, fmt.Sprintf("항목 %q: 토큰 형식 위반 — 제외", s.ID))
		case seen[s.ID]:
			c.Warnings = append(c.Warnings, fmt.Sprintf("항목 %q: id 중복 — 뒤 항목 무시", s.ID))
		default:
			seen[s.ID] = true
			c.Sessions = append(c.Sessions, CatalogSession{
				ID: s.ID, Name: s.Name, UnitName: s.UnitName,
				TokenLabel: s.TokenLabel, Token: s.PulseToken,
				TokenFP: s.PulseToken[:8], IssuedAt: s.IssuedAt,
			})
		}
	}
	if len(c.Sessions) == 0 {
		return nil, fmt.Errorf("유효한 세션이 없음")
	}
	return c, nil
}
