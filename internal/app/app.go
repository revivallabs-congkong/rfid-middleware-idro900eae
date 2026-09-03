// Package app 은 composition root 다 — 설정으로부터 전체 파이프라인을 조립하고
// 수명주기를 관리한다 (설계서 §3, §4).
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/admission"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/congkong"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gate"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/reader/session"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/replay"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/sender"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/store/sqlite"
)

type Options struct {
	Cfg *config.Config
	// Echo 가 nil 이 아니면 로그를 콘솔에도 출력한다 (foreground).
	Echo io.Writer
	// ReplayInput 이 nil 이 아니면 TCP 리더 대신 재생 입력을 사용한다.
	ReplayInput  io.Reader
	ReplayNDJSON bool
	ReplayReader string
	// DrainAndExit 는 replay 모드에서 입력 소진 + 큐 드레인 후 종료한다.
	DrainAndExit bool
	// Version/Mode 는 status.json v2 에 기록된다 (GUI 설계 §6.1).
	// Mode: "service" | "hosting" | "cli" (빈 값은 cli).
	Version string
	Mode    string
	// PreacquiredLock 이 nil 이 아니면 app.lock 획득을 생략하고 종료 시 이
	// release 를 호출한다 — GUI 호스팅 모드의 잠금 위임 (GUI 설계 §6.5).
	// AcquireLock 으로 얻은 release 를 그대로 넘긴다.
	PreacquiredLock func()
}

// AcquireLock 은 dataDir 의 app.lock 배타 잠금을 획득한다 (모드 중재용 —
// GUI 설계 §6.5). 성공 시 release 를 돌려주며, Run 의 PreacquiredLock 으로
// 넘기면 Run 은 재획득하지 않는다.
func AcquireLock(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("dataDir 생성 실패: %w", err)
	}
	return acquireLock(filepath.Join(dataDir, "app.lock"))
}

// AcquireNamedLock 은 지정 경로의 배타 잠금을 획득한다 (GUI 단일 인스턴스용).
func AcquireNamedLock(path string) (func(), error) {
	return acquireLock(path)
}

// Run 은 ctx 취소(또는 replay drain 완료)까지 전체 파이프라인을 실행한다.
// foreground 와 Windows Service 양쪽이 같은 이 함수를 호출한다 (설계서 §10).
func Run(ctx context.Context, opts Options) error {
	cfg := opts.Cfg

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("dataDir 생성 실패: %w", err)
	}
	release := opts.PreacquiredLock
	if release == nil {
		var err error
		release, err = acquireLock(filepath.Join(cfg.DataDir, "app.lock"))
		if err != nil {
			return err
		}
	}
	defer release()

	log, err := logging.New(filepath.Join(cfg.DataDir, "logs"), logging.ParseLevel(cfg.LogLevel), opts.Echo)
	if err != nil {
		return err
	}
	defer log.Close()

	if err := health.CheckDiskDir(cfg.DataDir); err != nil {
		return fmt.Errorf("dataDir 쓰기 불가: %w", err)
	}

	// NTP 점검 — checkedAt 신뢰의 전제 (R7).
	ntp := health.CheckNTP()
	switch {
	case !ntp.Supported:
		log.Infof("NTP_CHECK_SKIPPED", logging.F{"message": ntp.Detail})
	case !ntp.ServiceRunning || !ntp.QueryOK:
		log.Errorf("NTP_WARNING", logging.F{
			"message": "Windows Time 동기화를 확인하세요 — checkedAt 이 서버에서 400 으로 폐기될 수 있습니다",
			"serviceRunning": ntp.ServiceRunning, "queryOk": ntp.QueryOK,
		})
	default:
		log.Infof("NTP_OK", logging.F{"serviceRunning": true})
	}

	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "queue.db"))
	if err != nil {
		return fmt.Errorf("큐 DB 열기 실패: %w", err)
	}
	defer st.Close()

	clk := clock.Real{}
	gates := gate.NewRegistry()

	readerIDs := make([]string, 0, len(cfg.Readers))
	for _, r := range cfg.Readers {
		readerIDs = append(readerIDs, r.ID)
	}
	tele := newTelemetry(readerIDs, clk.Now())

	// 영속화된 gate 상태 복원 — suspension 은 재시작을 넘어 유지된다.
	rows, err := st.Gates()
	if err != nil {
		return fmt.Errorf("gate 상태 복원 실패: %w", err)
	}
	for _, r := range cfg.Readers {
		if row, ok := rows[r.ID]; ok {
			state := row.State
			// 정상/일시 상태는 preflight 로 다시 확정한다. suspended 는 유지.
			if !state.Suspended() {
				state = domain.GatePreflightPending
			}
			// SUSPENDED_TOKEN 예외: 운영자가 *다른* 토큰으로 바꿨고 대기 행이
			// 없으면, 회수된 옛 토큰의 suspension 은 의미가 없다 — 새 토큰을
			// preflight 로 재검증한다. (REBIND/CONFIG 나 대기 잔량이 있는 경우는
			// 오배송 위험이 있어 그대로 유지하고 명시적 resume 을 요구한다.)
			if state == domain.GateSuspendedToken && r.Token.Fingerprint() != row.TokenFingerprint {
				if pc, perr := st.PendingCount(r.ID); perr == nil && pc == 0 {
					log.Warnf("GATE_TOKEN_CHANGED_REVERIFY", logging.F{
						"readerId": r.ID, "message": "토큰 변경 + 대기 0건 — 회수 상태 해제 후 재검증",
					})
					state = domain.GatePreflightPending
					_ = st.SetGate(r.ID, state, "operator changed token",
						clk.Now().UnixMilli(), r.Token.Fingerprint(), nil)
				}
			}
			gates.Init(r.ID, gate.Entry{
				State: state, Reason: row.Reason,
				Fingerprint: row.TokenFingerprint, Meta: row.Meta,
			})
			if state.Suspended() {
				log.Errorf("GATE_RESTORED_SUSPENDED", logging.F{
					"readerId": r.ID, "gateState": string(state), "message": row.Reason,
				})
			}
		} else {
			gates.Init(r.ID, gate.Entry{State: domain.GatePreflightPending})
		}
	}

	client, err := congkong.New(cfg.APIHost, time.Duration(cfg.RequestTimeoutSec)*time.Second)
	if err != nil {
		return err
	}

	snd := sender.New(st, client, cfg, gates, clk, log, time.Duration(cfg.QueueMaxAgeHours)*time.Hour)
	snd.OnSuccess = func(id string) { tele.success(id, clk.Now()) }
	snd.OnServerContact = func(ok bool) { tele.serverContact(ok, clk.Now()) }
	adm := &admission.Service{
		Store: st, Gates: gates, Log: log,
		Debounce: time.Duration(cfg.DebounceSec) * time.Second,
		Wake:     snd.Wake,
		OnTag:    func(id string) { tele.tagSeen(id, clk.Now()) },
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup

	// preflight — reader 별 토큰 자기 검증 (B1)
	for _, r := range cfg.Readers {
		if e, ok := gates.Get(r.ID); ok && e.State.Suspended() {
			continue // 복원된 suspension 은 preflight 로 풀지 않는다 — 명시적 resume 필요
		}
		wg.Add(1)
		go func(r config.Reader) {
			defer wg.Done()
			p := &sender.Preflight{Client: client, Gates: gates, Store: st, Clock: clk, Log: log}
			p.Run(runCtx, r.ID, r.Token)
		}(r)
	}

	// sender — 전역 1개 (불변식 12)
	wg.Add(1)
	go func() {
		defer wg.Done()
		snd.Run(runCtx)
	}()

	// 입력: TCP 리더 세션 또는 재생
	replayDone := make(chan error, 1)
	if opts.ReplayInput != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner := &replay.Runner{ReaderID: opts.ReplayReader, Handler: adm.Handle, Log: log, Clock: clk}
			var rerr error
			if opts.ReplayNDJSON {
				rerr = runner.RunNDJSON(runCtx, opts.ReplayInput)
			} else {
				rerr = runner.RunRaw(runCtx, opts.ReplayInput)
			}
			replayDone <- rerr
		}()
	} else {
		for _, r := range cfg.Readers {
			wg.Add(1)
			go func(r config.Reader) {
				defer wg.Done()
				s := session.New(session.Config{
					ReaderID: r.ID, Addr: r.Addr,
					PowerGain: cfg.PowerGain, Buzzer: cfg.Buzzer,
					OnConn: func(up bool) { tele.conn(r.ID, up, clk.Now()) },
				}, adm.Handle, log, clk)
				s.Run(runCtx)
			}(r)
		}
	}

	// sweeper — 만료·디바운스 정리·만료 임박 경고 (설계서 §7.4)
	wg.Add(1)
	go func() {
		defer wg.Done()
		maxAge := time.Duration(cfg.QueueMaxAgeHours) * time.Hour
		debounce := time.Duration(cfg.DebounceSec) * time.Second
		keep := 2 * debounce
		if keep < 10*time.Minute {
			keep = 10 * time.Minute
		}
		for {
			select {
			case <-runCtx.Done():
				return
			case <-clk.After(time.Minute):
			}
			now := clk.Now()
			expired, err := st.ExpireBefore(now.Add(-maxAge).UnixMilli())
			if err != nil {
				log.Errorf("QUEUE_EXPIRE_FAILED", logging.F{"message": err.Error()})
			}
			for _, e := range expired {
				log.Warnf("QUEUE_ITEM_EXPIRED", logging.F{
					"readerId": e.ReaderID, "epc": e.EPC, "checkedAt": e.CheckedAt,
				})
			}
			if _, err := st.CleanupDebounce(now.Add(-keep).UnixMilli()); err != nil {
				log.Warnf("DEBOUNCE_CLEANUP_FAILED", logging.F{"message": err.Error()})
			}
			// 만료 임박(보관 상한의 90% 초과) 경고 (계획서 §10)
			if oldest, ok, _ := st.OldestCheckedAt(); ok {
				if t, perr := time.Parse(time.RFC3339Nano, oldest); perr == nil {
					if now.Sub(t) > maxAge*9/10 {
						log.Errorf("QUEUE_NEAR_EXPIRY", logging.F{
							"oldestCheckedAt": oldest,
							"message":         "만료 전 네트워크/서버 복구가 필요합니다",
						})
					}
				}
			}
		}
	}()

	// 반복 경보 — suspension/halt 를 사람이 발견할 수 있게 (계획서 §6.6)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-clk.After(time.Minute):
			}
			snap, global := gates.Snapshot()
			if global == domain.SenderHalted {
				log.Errorf("SENDER_HALTED_REQUEST_BUG", logging.F{
					"message": "요청 형식 치명 오류 — 수정 배포 후 queue resume 이 필요합니다",
				})
			}
			for id, e := range snap {
				if e.State.Suspended() {
					log.Errorf("GATE_SUSPENDED_ALERT", logging.F{
						"readerId": id, "gateState": string(e.State), "message": e.Reason,
					})
				}
			}
		}
	}()

	// 시계 skew 프로브 — 기동 시 + 1시간 주기 (GUI 설계 §6.9). preflight 의
	// Date 헤더 관측과 같은 원천이며, status ntp 로 승격된다.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if skew, ok := client.ProbeSkew(runCtx); ok {
				tele.setSkew(int(skew.Seconds()), clk.Now())
				tele.serverContact(true, clk.Now()) // Date 헤더 수신 = 서버 도달
			}
			select {
			case <-runCtx.Done():
				return
			case <-clk.After(time.Hour):
			}
		}
	}()

	// status snapshot v2 — 변경 이벤트 구동 + 5초 하트비트, atomic replace
	// (설계서 §12.3, GUI 설계 §6.1)
	statusPath := filepath.Join(cfg.DataDir, "status.json")
	mode := opts.Mode
	if mode == "" {
		mode = "cli"
	}
	startedAt := clk.Now().Format(time.RFC3339)
	fmtT := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	writeStatus := func() {
		snap, global := gates.Snapshot()
		depth, _ := st.Depth()
		oldest, _, _ := st.OldestCheckedAt()
		now := clk.Now()
		rsnap, success, ntpChecked, skewSec, skewAt := tele.snapshot()
		s := health.Status{
			Schema:             health.StatusSchema,
			UpdatedAt:          now.Format(time.RFC3339),
			SenderState:        string(global),
			QueueDepth:         depth,
			QueueNonEmptySince: fmtT(tele.queueDepthObserved(depth, now)),
			OldestCheckedAt:    oldest,
			PID:                os.Getpid(),
			Version:            opts.Version,
			Mode:               mode,
			StartedAt:          startedAt,
			SuccessSinceStart:  success,
		}
		if ntpChecked {
			s.NTP = &health.NTPInfo{Checked: true, SkewSec: skewSec, At: fmtT(skewAt)}
		}
		if seen, online, okAt := tele.server(); seen {
			s.Server = &health.ServerInfo{Seen: true, Online: online, LastOK: fmtT(okAt)}
		}
		for _, r := range cfg.Readers {
			e := snap[r.ID]
			rt := rsnap[r.ID]
			connState := "DISCONNECTED"
			if rt.connected {
				connState = "CONNECTED"
			}
			pending, _ := st.PendingCount(r.ID)
			s.Readers = append(s.Readers, health.ReaderStatus{
				ID: r.ID, GateState: string(e.State), GateReason: e.Reason,
				EventName: e.Meta.EventName, BoothName: e.Meta.BoothName,
				UnitName: e.Meta.UnitName, CooldownSec: e.Meta.CooldownSec,
				ConnState: connState, ConnSince: fmtT(rt.connSince),
				SessionID: r.SessionID, Pending: pending,
				LastTagAt: fmtT(rt.lastTagAt), LastSuccessAt: fmtT(rt.lastSuccessAt),
			})
		}
		if err := health.WriteStatus(statusPath, s); err != nil {
			log.Warnf("STATUS_WRITE_FAILED", logging.F{"message": err.Error()})
		}
	}
	gateCh, gateCancel := gates.Subscribe()
	defer gateCancel()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			writeStatus()
			select {
			case <-runCtx.Done():
				writeStatus()
				return
			case <-gateCh:
			case <-clk.After(5 * time.Second):
			}
		}
	}()

	log.Infof("MIDDLEWARE_STARTED", logging.F{
		"readers": len(cfg.Readers), "replay": opts.ReplayInput != nil,
	})

	// 종료 대기
	if opts.ReplayInput != nil && opts.DrainAndExit {
		select {
		case <-ctx.Done():
		case rerr := <-replayDone:
			if rerr != nil && rerr != context.Canceled {
				log.Errorf("REPLAY_FAILED", logging.F{"message": rerr.Error()})
			}
			// 입력 소진 후 큐 드레인(또는 더 진행 불가) 대기
			drainWait(ctx, st, gates, clk)
		}
	} else {
		<-ctx.Done()
	}

	// 정상 종료: 새 입력을 막고 진행 중 트랜잭션을 끝낸다. 처리 중 큐 항목은
	// 삭제하지 않아 다음 기동에서 안전하게 재시도한다 (계획서 §5.2).
	cancel()
	wg.Wait()
	log.Infof("MIDDLEWARE_STOPPED", logging.F{})
	return nil
}

// drainWait 는 sendable 큐가 빌 때까지(또는 진행이 불가능해질 때까지) 기다린다.
func drainWait(ctx context.Context, st *sqlite.Store, gates *gate.Registry, clk clock.Clock) {
	idle := 0
	var lastDepth int64 = -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-clk.After(200 * time.Millisecond):
		}
		if gates.GlobalState() == domain.SenderHalted {
			return
		}
		sendable := gates.SendableReaders()
		depth, err := st.Depth()
		if err != nil {
			return
		}
		if depth == 0 {
			return
		}
		if len(sendable) == 0 {
			// preflight 진행 중일 수 있으므로 잠시 기다리되, 오래 멈추면 종료
			idle++
			if idle > 150 { // 30초
				return
			}
			continue
		}
		// 남은 행이 전부 미래 backoff 면 드레인 종료로 본다 (재시작 시 재생됨)
		dueMS, ok, _ := st.NextDueAtMS(sendable)
		if ok && dueMS > clk.Now().Add(2*time.Second).UnixMilli() && depth == lastDepth {
			return
		}
		lastDepth = depth
	}
}
