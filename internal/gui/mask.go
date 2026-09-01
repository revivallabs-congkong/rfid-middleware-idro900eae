package gui

import "regexp"

// 브라우저로 나가는 로그 라인의 이중 방어 마스킹 (GUI 설계 §2.4).
// 코어 로깅이 이미 토큰·PII 를 배제하지만, 송출 직전 한 번 더 거른다.
var (
	// 64자리 hex(토큰 형태) → 앞 8자만
	tokenLike = regexp.MustCompile(`[0-9a-fA-F]{64}`)
	// "epc":"...HEX..." → 끝 4자 마스킹
	epcField = regexp.MustCompile(`("epc"\s*:\s*")([0-9A-Fa-f]{6,})([0-9A-Fa-f]{4})(")`)
)

func maskLogLine(line []byte) []byte {
	out := tokenLike.ReplaceAllFunc(line, func(m []byte) []byte {
		return append(append([]byte{}, m[:8]...), []byte("…")...)
	})
	out = epcField.ReplaceAll(out, []byte(`${1}${2}****${4}`))
	return out
}
