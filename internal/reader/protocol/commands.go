package protocol

import "fmt"

// 명령 인코더 — 문자열 결합을 호출부에서 하지 않고 허용 목록만 제공한다 (설계서 §6.3).
// EOL 은 반드시 CR(0x0D) 단독이다. CRLF 로 보내면 리더가 명령을 조용히 무시한다.
// Kill(>k)·Lock(>l)·Untraceable(>u) 인코더는 만들지 않는다 — 비가역 명령 (settings §6.7).

// CmdVersion 은 펌웨어 버전 조회다: 3E 79 20 76 0D. 기대 응답 >v {value}.
func CmdVersion() []byte { return []byte{0x3E, 0x79, 0x20, 0x76, 0x0D} }

// CmdSetBuzzer 는 부저 설정이다. 기대 응답 >b {v}.
func CmdSetBuzzer(v int) ([]byte, error) {
	if v != 0 && v != 1 {
		return nil, fmt.Errorf("buzzer 는 0 또는 1: %d", v)
	}
	return []byte(fmt.Sprintf(">x b %d\r", v)), nil
}

// CmdSetPower 는 출력 설정이다 (50~300, 0.1dBm 단위). 기대 응답 >p {v}.
func CmdSetPower(v int) ([]byte, error) {
	if v < 50 || v > 300 {
		return nil, fmt.Errorf("powerGain 은 50~300: %d", v)
	}
	return []byte(fmt.Sprintf(">x p %d\r", v)), nil
}

// CmdRSSIOff 는 Packet Option 0 이다 — v1 은 RSSI 를 쓰지 않는다 (dev-spec §3).
// 기대 응답 >i 0. 파서는 ;R 접미가 있는 과거 설정의 라인도 계속 허용한다.
func CmdRSSIOff() []byte { return []byte(">x i 0\r") }

// CmdStartInventory 는 Inventory 시작이다: 3E 66 0D. 기대 응답 >Af.
func CmdStartInventory() []byte { return []byte{0x3E, 0x66, 0x0D} }

// CmdStop 은 실행 중 명령 즉시 종료다: 33 0D — 유일하게 헤더 > 가 없다 (불변식 8).
// 기대 응답 >A3.
func CmdStop() []byte { return []byte{0x33, 0x0D} }

// Matcher 는 기대 응답 판정 함수다.
type Matcher func(Line) bool

// MatchSetting 은 >{t} {any} 설정 응답이다.
func MatchSetting(t byte) Matcher {
	return func(l Line) bool { return l.Kind == KindSetting && l.Setting.Type == t }
}

// MatchAck 는 >A{c} 다. Stop 은 MatchAck('3').
func MatchAck(c byte) Matcher {
	return func(l Line) bool { return l.Kind == KindAck && l.Ack == c }
}
