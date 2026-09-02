package session

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/reader/protocol"
)

// ProbeResult 는 1회 리더 진단 결과다 (GUI 설계 §6.2, 마법사 1단계).
type ProbeResult struct {
	Firmware string
	RTT      time.Duration
}

// Probe 는 접속 → Stop(자동 인벤토리 정리) → 버전 조회 → 종료의 1회 왕복
// 진단이다. Run 과 달리 재접속 루프·Inventory 진입을 하지 않으며, admission
// 에도 아무것도 넘기지 않는다. 코어가 리더를 점유 중일 때는 호출하지 말 것 —
// 호출자는 status 의 connState 로 대체 판정한다 (§5.3 #1).
func Probe(ctx context.Context, addr string, dial func(ctx context.Context) (net.Conn, error)) (ProbeResult, error) {
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, dialTimeout+2*cmdTimeout+stopSettle)
	defer cancel()

	var conn net.Conn
	var err error
	if dial != nil {
		conn, err = dial(pctx)
	} else {
		d := net.Dialer{Timeout: dialTimeout}
		conn, err = d.DialContext(pctx, "tcp", addr)
	}
	if err != nil {
		return ProbeResult{}, errors.New("리더 접속 실패: " + err.Error())
	}
	defer conn.Close()

	chunks := make(chan []byte, 64)
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case chunks <- cp:
				case <-pctx.Done():
					return
				}
			}
			if rerr != nil {
				readErr <- rerr
				return
			}
		}
	}()
	stopClose := context.AfterFunc(pctx, func() { conn.Close() })
	defer stopClose()

	framer := protocol.NewFramer(protocol.DefaultMaxLine)

	// Stop — 자동 인벤토리(x=1) 잔류 스트림을 비운다 (실장비 관측 2026-09-02).
	conn.SetWriteDeadline(time.Now().Add(cmdTimeout))
	conn.Write(protocol.CmdStop())
	conn.SetWriteDeadline(time.Time{})
	settle := time.After(stopSettle)
	ackStop := protocol.MatchAck('3')
drain:
	for {
		select {
		case <-pctx.Done():
			return ProbeResult{}, pctx.Err()
		case rerr := <-readErr:
			return ProbeResult{}, errors.New("연결 종료: " + rerr.Error())
		case <-settle:
			break drain
		case chunk := <-chunks:
			for _, raw := range framer.Push(chunk).Lines {
				if ackStop(protocol.Parse(raw)) {
					break drain
				}
			}
		}
	}

	// 버전 조회
	conn.SetWriteDeadline(time.Now().Add(cmdTimeout))
	if _, err := conn.Write(protocol.CmdVersion()); err != nil {
		return ProbeResult{}, errors.New("버전 명령 전송 실패: " + err.Error())
	}
	conn.SetWriteDeadline(time.Time{})
	matchV := protocol.MatchSetting('v')
	deadline := time.After(cmdTimeout)
	for {
		select {
		case <-pctx.Done():
			return ProbeResult{}, pctx.Err()
		case rerr := <-readErr:
			return ProbeResult{}, errors.New("버전 응답 전 연결 종료: " + rerr.Error())
		case <-deadline:
			return ProbeResult{}, errors.New("버전 응답 timeout (다른 프로그램 점유 또는 링크 이상)")
		case chunk := <-chunks:
			for _, raw := range framer.Push(chunk).Lines {
				line := protocol.Parse(raw)
				if matchV(line) {
					return ProbeResult{Firmware: line.Setting.Value, RTT: time.Since(start)}, nil
				}
			}
		}
	}
}
