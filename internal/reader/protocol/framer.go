// Package protocol 은 IDRO900EAE 의 TCP/ASCII 프로토콜을 다룬다.
// SSOT: docs/features/pulse/IDRO900EAE-settings.md
// 명령 EOL 은 CR 단독, 응답 프레임은 CRLF 다 (불변식 7).
package protocol

import "bytes"

var crlf = []byte{0x0D, 0x0A}

// DefaultMaxLine 은 한 라인 payload 상한이다 (설계서 §6.1: 4 KiB).
const DefaultMaxLine = 4096

// FrameResult 는 Push 한 chunk 에서 복원된 완전한 라인들이다.
type FrameResult struct {
	Lines   [][]byte
	Dropped int // 4 KiB 초과로 버린 frame 수 (READER_LINE_TOO_LONG)
}

// Framer 는 TCP 패킷 경계와 무관한 누적 버퍼에서 CRLF 프레임을 복원한다.
// bufio.Scanner 의 암묵적 제한에 의존하지 않는다 (설계서 §6.1).
type Framer struct {
	buf      []byte
	maxLine  int
	dropping bool
}

func NewFramer(maxLine int) *Framer {
	if maxLine <= 0 {
		maxLine = DefaultMaxLine
	}
	return &Framer{maxLine: maxLine}
}

// Push 는 수신 chunk 를 누적하고 완성된 라인들을 돌려준다.
// 한 chunk 의 0개/1개/여러 frame, CR 과 LF 사이 분할을 모두 처리한다.
func (f *Framer) Push(chunk []byte) FrameResult {
	f.buf = append(f.buf, chunk...)
	var res FrameResult
	for {
		i := bytes.Index(f.buf, crlf)
		if i < 0 {
			// delimiter 없이 상한 초과 — 이 frame 은 버리는 중 상태로 전환하고
			// delimiter 분할 대비 마지막 CR 후보만 남긴다.
			if len(f.buf) > f.maxLine {
				f.dropping = true
				if f.buf[len(f.buf)-1] == 0x0D {
					f.buf = append(f.buf[:0], 0x0D)
				} else {
					f.buf = f.buf[:0]
				}
			}
			return res
		}
		line := f.buf[:i]
		switch {
		case f.dropping:
			// 버리던 과대 frame 의 끝 — 다음 frame 부터 복구한다.
			f.dropping = false
			res.Dropped++
		case len(line) > f.maxLine:
			res.Dropped++
		default:
			cp := make([]byte, len(line))
			copy(cp, line)
			res.Lines = append(res.Lines, cp)
		}
		f.buf = append(f.buf[:0], f.buf[i+2:]...)
	}
}

// PendingTail 은 아직 완성되지 않은 잔여 바이트 수다.
// EOF 때 미완성 tail 은 파싱하지 않고 길이만 로그한다 (설계서 §6.1).
func (f *Framer) PendingTail() int { return len(f.buf) }
