package protocol

import (
	"bytes"
	"strconv"
)

// Kind 는 응답 라인 분류다 (settings §3.1: 두 번째 문자로 분기).
type Kind int

const (
	KindUnknown Kind = iota
	KindTag          // >T — Inventory Data Reply
	KindBadTag       // >T 이지만 PC/EPC/RSSI 형식 위반 — 카운트만, 세션 유지
	KindAck          // >A{cmd}
	KindCode         // >C — 결과/에러 코드
	KindSetting      // >{type} {value} — Get/Set Control 응답
)

// Tag 는 파싱된 >T 라인이다. EPC 는 대문자·무구분자, PC/RSSI 제외 (dev-spec §2).
type Tag struct {
	PC  string
	EPC string
	// RSSI 는 signed 16bit, 0.1 dBm 단위 (settings §3.2). v1 은 사용하지 않지만
	// fixture 회귀를 위해 올바르게 파싱한다. admission 에는 넘기지 않는다.
	RSSI    int16
	HasRSSI bool
}

// RSSIdBm 은 RSSI 를 dBm 값으로 돌려준다 (예: FD96 → -61.8).
func (t Tag) RSSIdBm() float64 { return float64(t.RSSI) / 10 }

// Setting 은 설정 응답이다 (예: >p 300 → Type 'p', Value "300").
type Setting struct {
	Type  byte
	Value string
}

// Line 은 분류된 응답 1건이다.
type Line struct {
	Kind    Kind
	Raw     string
	Tag     Tag     // KindTag
	Ack     byte    // KindAck: 명령 문자 ('f', '3' 등)
	Code    string  // KindCode: '>C' 뒤 원문
	Setting Setting // KindSetting
	Err     string  // KindBadTag/KindUnknown 사유
}

// Parse 는 CRLF 가 제거된 한 라인을 분류한다. 어떤 입력에도 panic 하지 않는다.
func Parse(raw []byte) Line {
	l := Line{Raw: string(raw)}
	if len(raw) < 2 || raw[0] != '>' {
		l.Kind = KindUnknown
		l.Err = "not-a-reply"
		return l
	}
	switch raw[1] {
	case 'T':
		return parseTag(raw)
	case 'A':
		rest := bytes.TrimSpace(raw[2:])
		if len(rest) < 1 {
			l.Kind = KindUnknown
			l.Err = "empty-ack"
			return l
		}
		l.Kind = KindAck
		l.Ack = rest[0]
		return l
	case 'C':
		l.Kind = KindCode
		l.Code = string(bytes.TrimSpace(raw[2:]))
		return l
	default:
		// >{type} {value} 형식만 설정 응답으로 본다.
		if len(raw) >= 4 && raw[2] == ' ' {
			l.Kind = KindSetting
			l.Setting = Setting{Type: raw[1], Value: string(bytes.TrimSpace(raw[3:]))}
			return l
		}
		l.Kind = KindUnknown
		l.Err = "unclassified"
		return l
	}
}

// parseTag 는 설계서 §6.2 의 순서를 그대로 따른다:
// 1) >T 확인 2) 선택적 ;R+4hex 분리 3) ASCII whitespace 제거
// 4) 처음 4 hex = PC 5) 나머지 비어 있지 않은 짝수 hex = EPC 6) 대문자 반환
func parseTag(raw []byte) Line {
	l := Line{Raw: string(raw)}
	payload := raw[2:]

	var rssi int16
	hasRSSI := false
	if i := bytes.Index(payload, []byte(";R")); i >= 0 {
		suffix := bytes.TrimRight(payload[i+2:], " \t")
		if len(suffix) != 4 || !isHexBytes(suffix) {
			l.Kind = KindBadTag
			l.Err = "bad-rssi"
			return l
		}
		u, err := strconv.ParseUint(string(suffix), 16, 16)
		if err != nil {
			l.Kind = KindBadTag
			l.Err = "bad-rssi"
			return l
		}
		// 반드시 부호 있는 정수로 캐스팅 (settings §3.2 — unsigned 로 읽으면 오류값)
		rssi = int16(u)
		hasRSSI = true
		payload = payload[:i]
	}

	payload = stripSpaces(payload)
	if len(payload) < 6 { // PC 4자 + EPC 최소 2자
		l.Kind = KindBadTag
		l.Err = "too-short"
		return l
	}
	pc := payload[:4]
	epc := payload[4:]
	if !isHexBytes(pc) {
		l.Kind = KindBadTag
		l.Err = "bad-pc"
		return l
	}
	// EPC 는 가변 길이·짝수 자리·비어 있지 않은 hex. 고정 길이를 가정하지 않는다.
	// ':' '-' '.' 같은 구분자는 리더 EPC 에서는 거부한다 (설계서 §6.2).
	if len(epc)%2 != 0 || !isHexBytes(epc) {
		l.Kind = KindBadTag
		l.Err = "bad-epc"
		return l
	}
	l.Kind = KindTag
	l.Tag = Tag{PC: upperHex(pc), EPC: upperHex(epc), RSSI: rssi, HasRSSI: hasRSSI}
	return l
}

func stripSpaces(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == ' ' || c == '\t' {
			continue
		}
		out = append(out, c)
	}
	return out
}

func isHexBytes(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func upperHex(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
