package gui

import "regexp"

// 브라우저로 나가는 로그 라인의 이중 방어 마스킹 (GUI 설계 §2.4).
// 토큰(64hex)은 절대 브라우저로 내보내지 않는다. EPC(태그값)는 운영 스탭이
// 체크인 확인에 필요하므로 마스킹하지 않는다(사용자 요청 2026-09-03).
var (
	// 64자리 hex(토큰 형태) → 앞 8자만
	tokenLike = regexp.MustCompile(`[0-9a-fA-F]{64}`)
)

func maskLogLine(line []byte) []byte {
	return tokenLike.ReplaceAllFunc(line, func(m []byte) []byte {
		return append(append([]byte{}, m[:8]...), []byte("…")...)
	})
}
