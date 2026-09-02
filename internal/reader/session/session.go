// Package session 은 리더 TCP 접속·초기화·Inventory·재접속 상태 머신이다 (설계서 §6.4).
package session

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/reader/protocol"
)

const (
	// cmdTimeout — 각 INIT 명령의 write+응답 deadline (계획서 §6.1: 2초).
	cmdTimeout  = 2 * time.Second
	dialTimeout = 5 * time.Second
)

// reconnectDelay — 실패마다 1s → 5s → 30s, 이후 30s (dev-spec §3).
func reconnectDelay(attempt int) time.Duration {
	switch {
	case attempt <= 0:
		return time.Second
	case attempt == 1:
		return 5 * time.Second
	default:
		return 30 * time.Second
	}
}

// Handler 는 Inventory 중 수신된 TagRead 를 받는다 (admission.Handle).
type Handler func(domain.TagRead)

type Config struct {
	ReaderID  string
	Addr      string
	PowerGain int
	Buzzer    int
	// OnConn 은 TCP 연결 확립/종료를 보고한다 (선택 — status 관측용,
	// GUI 설계 §6.6). 짧게 반환해야 한다.
	OnConn func(connected bool)
}

type Session struct {
	Cfg     Config
	Handler Handler
	Log     *logging.Logger
	Clock   clock.Clock
	// Dial 은 테스트 주입용이다. nil 이면 net.Dialer 를 쓴다.
	Dial func(ctx context.Context) (net.Conn, error)

	reconfig chan int // 새 powerGain 요청 (Stop→설정→재개 경로 검증용)
}

func New(cfg Config, handler Handler, log *logging.Logger, clk clock.Clock) *Session {
	return &Session{Cfg: cfg, Handler: handler, Log: log, Clock: clk, reconfig: make(chan int, 1)}
}

// RequestPower 는 Inventory 중 Stop→설정→`>f` 직렬화 경로로 파워를 바꾼다 (RDR2).
// v1 운영 설정 반영은 재시작이지만(ADR-007), 경로 자체는 유지·검증한다.
func (s *Session) RequestPower(v int) {
	select {
	case s.reconfig <- v:
	default:
	}
}

// Run 은 ctx 취소까지 접속→초기화→Inventory→재접속을 반복한다.
func (s *Session) Run(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := s.dial(ctx)
		if err != nil {
			s.Log.Warnf("READER_CONNECT_FAILED", logging.F{
				"readerId": s.Cfg.ReaderID, "readerAddr": s.Cfg.Addr, "message": err.Error(),
			})
			if !s.sleep(ctx, reconnectDelay(attempt)) {
				return
			}
			attempt++
			continue
		}
		s.Log.Infof("READER_CONNECTED", logging.F{"readerId": s.Cfg.ReaderID, "readerAddr": s.Cfg.Addr})
		if s.Cfg.OnConn != nil {
			s.Cfg.OnConn(true)
		}

		inventoryEntered, err := s.serve(ctx, conn)
		conn.Close()
		if s.Cfg.OnConn != nil {
			s.Cfg.OnConn(false)
		}
		if ctx.Err() != nil {
			return
		}
		s.Log.Warnf("READER_DISCONNECTED", logging.F{
			"readerId": s.Cfg.ReaderID, "readerAddr": s.Cfg.Addr, "message": errString(err),
		})
		if inventoryEntered {
			// Inventory 진입 성공했던 세션 — backoff 초기화 (설계서 §6.4).
			attempt = 0
		}
		if !s.sleep(ctx, reconnectDelay(attempt)) {
			return
		}
		attempt++
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Session) dial(ctx context.Context) (net.Conn, error) {
	if s.Dial != nil {
		return s.Dial(ctx)
	}
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 15 * time.Second}
	return d.DialContext(ctx, "tcp", s.Cfg.Addr)
}

// serve 는 한 TCP 세션의 전체 수명이다. Inventory 진입 여부와 종료 원인을 돌려준다.
func (s *Session) serve(ctx context.Context, conn net.Conn) (inventoryEntered bool, err error) {
	// 별도 goroutine 이 chunk 를 퍼올린다 — ctx 취소는 conn.Close 로 Read 를 깨운다.
	chunks := make(chan []byte, 64)
	readErr := make(chan error, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case chunks <- cp:
				case <-ctx.Done():
					return
				}
			}
			if rerr != nil {
				readErr <- rerr
				return
			}
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	framer := protocol.NewFramer(protocol.DefaultMaxLine)
	st := &sessionState{s: s, conn: conn, framer: framer, chunks: chunks, readErr: readErr, inInit: true}

	if err := st.initialize(ctx); err != nil {
		return false, err
	}
	st.inInit = false
	s.Log.Infof("READER_INVENTORY_STARTED", logging.F{"readerId": s.Cfg.ReaderID})

	// INVENTORY 루프
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case rerr := <-readErr:
			return true, rerr
		case chunk := <-chunks:
			st.handleChunk(chunk)
		case power := <-s.reconfig:
			if err := st.reconfigure(ctx, power); err != nil {
				return true, err
			}
		}
	}
}

type sessionState struct {
	s       *Session
	conn    net.Conn
	framer  *protocol.Framer
	chunks  chan []byte
	readErr chan error
	inInit  bool
}

type initStep struct {
	name  string
	cmd   []byte
	match protocol.Matcher
}

func (st *sessionState) initSteps() ([]initStep, error) {
	buzzer, err := protocol.CmdSetBuzzer(st.s.Cfg.Buzzer)
	if err != nil {
		return nil, err
	}
	power, err := protocol.CmdSetPower(st.s.Cfg.PowerGain)
	if err != nil {
		return nil, err
	}
	// 확정 초기화 시퀀스 (dev-spec §3). RSSI 는 v1 에서 끈다.
	return []initStep{
		{"INIT_VERSION", protocol.CmdVersion(), protocol.MatchSetting('v')},
		{"INIT_BUZZER", buzzer, protocol.MatchSetting('b')},
		{"INIT_POWER", power, protocol.MatchSetting('p')},
		{"INIT_RSSI_OFF", protocol.CmdRSSIOff(), protocol.MatchSetting('i')},
		{"STARTING_INVENTORY", protocol.CmdStartInventory(), protocol.MatchAck('f')},
	}, nil
}

// stopSettle — Stop 후 인벤토리 잔류 스트림을 비우는 최대 대기. Stop ack(>A3)
// 를 받으면 조기 종료한다.
const stopSettle = 700 * time.Millisecond

// stopAndDrain 은 INIT 전에 Stop(3\r) 을 보내 자동 인벤토리를 멈추고 잔류
// 태그 스트림을 흡수한다 (settings §3.2 "Inventory 중이면 Stop 먼저").
// 리더가 Reader Mode x=1(부팅 시 자동 Inventory)이면 접속 즉시 >T 를 쏟아내
// INIT_VERSION 의 >v 응답을 묻어 timeout 을 유발하므로, 초기화 선행 단계로
// 반드시 필요하다 (실장비 관측 2026-09-02). 인벤토리가 아니어도 무해하다.
func (st *sessionState) stopAndDrain(ctx context.Context) error {
	st.conn.SetWriteDeadline(time.Now().Add(cmdTimeout))
	if _, err := st.conn.Write(protocol.CmdStop()); err != nil {
		return errors.New("STOP write 실패: " + err.Error())
	}
	st.conn.SetWriteDeadline(time.Time{})

	settle := st.s.Clock.After(stopSettle)
	ackStop := protocol.MatchAck('3')
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case rerr := <-st.readErr:
			return errors.New("STOP 중 연결 종료: " + errString(rerr))
		case <-settle:
			return nil
		case chunk := <-st.chunks:
			res := st.framer.Push(chunk)
			st.logDropped(res.Dropped)
			for _, raw := range res.Lines {
				if ackStop(protocol.Parse(raw)) {
					return nil // Stop 확인 — 잔류 비움 완료
				}
				// 그 외(잔류 >T 등)는 버린다 — INIT 전이라 admission 대상 아님
			}
		}
	}
}

func (st *sessionState) initialize(ctx context.Context) error {
	if err := st.stopAndDrain(ctx); err != nil {
		return err
	}
	steps, err := st.initSteps()
	if err != nil {
		return err
	}
	for _, step := range steps {
		if err := st.command(ctx, step.name, step.cmd, step.match); err != nil {
			return err
		}
		if step.name == "INIT_VERSION" {
			// 버전 응답은 command() 안에서 SettingReply 로그로 남는다.
			continue
		}
	}
	return nil
}

// command 는 write 후 matcher 응답을 deadline 안에 기다린다.
// timeout 때 같은 명령을 재전송하지 않고 세션을 실패시킨다 — `>f` 실행 후
// ACK 만 유실된 경우의 중복 명령을 피하기 위해서다 (설계서 §6.4).
func (st *sessionState) command(ctx context.Context, name string, cmd []byte, match protocol.Matcher) error {
	st.conn.SetWriteDeadline(time.Now().Add(cmdTimeout))
	if _, err := st.conn.Write(cmd); err != nil {
		return errors.New(name + " write 실패: " + err.Error())
	}
	st.conn.SetWriteDeadline(time.Time{})

	deadline := st.s.Clock.After(cmdTimeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case rerr := <-st.readErr:
			return errors.New(name + " 중 연결 종료: " + errString(rerr))
		case <-deadline:
			return errors.New(name + " 응답 timeout")
		case chunk := <-st.chunks:
			if st.consumeUntil(chunk, match) {
				return nil
			}
		}
	}
}

// consumeUntil 은 chunk 의 라인들을 처리하며 matcher 일치를 찾는다.
func (st *sessionState) consumeUntil(chunk []byte, match protocol.Matcher) bool {
	res := st.framer.Push(chunk)
	st.logDropped(res.Dropped)
	found := false
	for _, raw := range res.Lines {
		line := protocol.Parse(raw)
		if match != nil && !found && match(line) {
			st.logLine(line)
			found = true
			continue
		}
		st.dispatch(line)
	}
	return found
}

func (st *sessionState) handleChunk(chunk []byte) {
	res := st.framer.Push(chunk)
	st.logDropped(res.Dropped)
	for _, raw := range res.Lines {
		st.dispatch(protocol.Parse(raw))
	}
}

func (st *sessionState) logDropped(n int) {
	for i := 0; i < n; i++ {
		st.s.Log.Warnf("READER_LINE_TOO_LONG", logging.F{"readerId": st.s.Cfg.ReaderID})
	}
}

// dispatch 는 기대 응답이 아닌 라인을 처리한다. 어떤 라인도 세션을 죽이지 않는다.
func (st *sessionState) dispatch(line protocol.Line) {
	switch line.Kind {
	case protocol.KindTag:
		if st.inInit {
			// INIT 완료 전 TagReply 는 오래된 Inventory 일 수 있으므로
			// admission 에 넣지 않는다 (불변식 9).
			st.s.Log.Debugf("READER_RAW", logging.F{
				"readerId": st.s.Cfg.ReaderID, "raw": line.Raw, "message": "init 중 수신 — 무시",
			})
			return
		}
		// receivedAt 은 완전한 >T 라인을 파싱한 직후의 시각이다 (계획서 §6.2).
		st.s.Handler(domain.TagRead{
			ReaderID:   st.s.Cfg.ReaderID,
			EPC:        line.Tag.EPC,
			ReceivedAt: st.s.Clock.Now(),
			RawLine:    line.Raw,
		})
	case protocol.KindBadTag:
		st.s.Log.Warnf("READER_BAD_TAG", logging.F{
			"readerId": st.s.Cfg.ReaderID, "raw": line.Raw, "message": line.Err,
		})
	case protocol.KindAck, protocol.KindCode, protocol.KindSetting:
		st.logLine(line)
	default:
		st.s.Log.Warnf("READER_UNKNOWN_LINE", logging.F{
			"readerId": st.s.Cfg.ReaderID, "raw": line.Raw,
		})
	}
}

func (st *sessionState) logLine(line protocol.Line) {
	// 리더 raw 라인은 PII 가 없어 보존한다 (dev-spec §3).
	st.s.Log.Debugf("READER_RAW", logging.F{"readerId": st.s.Cfg.ReaderID, "raw": line.Raw})
}

// reconfigure 는 정확히 Stop → ACK → 설정/응답 → >f 순서로 직렬화한다 (계획서 §6.1).
func (st *sessionState) reconfigure(ctx context.Context, power int) error {
	cmd, err := protocol.CmdSetPower(power)
	if err != nil {
		st.s.Log.Errorf("RECONFIGURE_REJECTED", logging.F{"readerId": st.s.Cfg.ReaderID, "message": err.Error()})
		return nil
	}
	st.s.Log.Infof("RECONFIGURE_START", logging.F{"readerId": st.s.Cfg.ReaderID, "powerGain": power})
	if err := st.command(ctx, "STOPPING", protocol.CmdStop(), protocol.MatchAck('3')); err != nil {
		return err
	}
	if err := st.command(ctx, "APPLYING", cmd, protocol.MatchSetting('p')); err != nil {
		return err
	}
	if err := st.command(ctx, "STARTING_INVENTORY", protocol.CmdStartInventory(), protocol.MatchAck('f')); err != nil {
		return err
	}
	st.s.Log.Infof("RECONFIGURE_DONE", logging.F{"readerId": st.s.Cfg.ReaderID, "powerGain": power})
	return nil
}

func (s *Session) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.Clock.After(d):
		return true
	}
}
