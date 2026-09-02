package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/app"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

type Options struct {
	ConfigPath string
	Version    string
}

// guiApp 은 GUI 오케스트레이터다: 모드 중재(§6.5), 카탈로그(§3),
// 세션 적용·재개(§3.4·§3.6)를 관장한다.
type guiApp struct {
	cfgPath string
	version string
	mode    string // hosting | observer
	dataDir string
	maxAge  int

	srv  *Server
	mgr  *CatalogManager
	core *CoreController // hosting 전용, observer 는 nil
	ring *LogRing

	mu  sync.Mutex
	cfg *config.Config // 디스크 설정의 현재 뷰 (쓰기 후 reload)
}

// Run 은 GUI 를 기동한다. app.lock 획득 여부로 호스팅/관측을 분기한다 (§7.1).
func Run(ctx context.Context, opts Options) error {
	ga := &guiApp{cfgPath: opts.ConfigPath, version: opts.Version, mode: "observer"}
	meta := Meta{Version: opts.Version, ConfigPath: opts.ConfigPath}

	cfg, cfgErr := config.Load(opts.ConfigPath)
	if cfgErr != nil {
		meta.ConfigError = cfgErr.Error()
	} else {
		ga.cfg = cfg
		ga.dataDir = cfg.DataDir
		ga.maxAge = cfg.QueueMaxAgeHours
		meta.DataDir = cfg.DataDir
		meta.CfgFingerprint = CfgFingerprint(cfg)
		// 모드 중재 — 잠금이 비어 있으면 호스팅 (§6.5)
		if release, err := app.AcquireLock(cfg.DataDir); err == nil {
			release() // 확인만 하고 놓는다 — Start 가 다시 잡는다
			ga.mode = "hosting"
		}
	}
	meta.Mode = ga.mode

	srv, err := NewServer(meta)
	if err != nil {
		return fmt.Errorf("로컬 HTTP 기동 실패: %w", err)
	}
	ga.srv = srv

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if ga.cfg != nil {
		ga.mgr = &CatalogManager{
			Path:         ga.catalogPath(),
			DownloadsDir: downloadsDir(),
			OnChange: func(reason string) {
				b, _ := json.Marshal(map[string]string{"reason": reason})
				srv.broadcast(sseMsg{event: "catalog", data: b})
			},
		}
		go ga.mgr.Run(runCtx)
	}

	if ga.mode == "hosting" {
		ga.ring = NewLogRing(4096)
		ga.core = &CoreController{CfgPath: ga.cfgPath, Version: ga.version, Ring: ga.ring}
		if err := ga.core.Start(runCtx); err != nil {
			// 기동 실패해도 GUI 는 떠서 원인을 보여준다
			ga.core.lastErr = err.Error()
		}
		defer ga.core.Stop() // 종료 grace 10s (§7.3)
		go ga.flushRing(runCtx)
	} else if ga.dataDir != "" {
		go tailLogs(runCtx, ga.dataDir, srv)
	}

	srv.ServiceControl = serviceControl(opts.ConfigPath)
	srv.Hooks = Hooks{
		ApplySession: ga.applySession,
		Resume:       ga.resume,
		CoreControl:  ga.coreControl,
		CatalogView:  ga.catalogView,
		CatalogOp:    ga.catalogOp,
	}

	go srv.Serve()
	if ga.dataDir != "" {
		go ga.stateLoop(runCtx)
	}
	OpenBrowser(srv.URL())

	runTray(runCtx, cancel, srv.URL(), func() bool {
		// 호스팅 모드 종료는 확인을 거친다 (§7.3 — 태그 유실 방지 R1)
		if ga.mode != "hosting" || ga.core == nil || !ga.core.Running() {
			return true
		}
		return confirmQuit()
	})
	return nil
}

func (ga *guiApp) catalogPath() string {
	p := ga.cfg.SessionsFile
	if p == "" {
		p = "pulse-sessions.json"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(ga.cfgPath), p)
	}
	return p
}

func (ga *guiApp) flushRing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ga.ring.Notify():
			for _, line := range ga.ring.Drain() {
				ga.srv.PushLog(line)
			}
		}
	}
}

// stateLoop 은 status.json → State 계산 → SSE 푸시다. 호스팅은 300ms
// (G4: 스캔→표시 ≤500ms), 관측은 1s.
func (ga *guiApp) stateLoop(ctx context.Context) {
	interval := time.Second
	if ga.mode == "hosting" {
		interval = 300 * time.Millisecond
	}
	statusPath := filepath.Join(ga.dataDir, "status.json")
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var lastKey string
	var lastPush time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		s, err := health.ReadStatus(statusPath)
		st := BuildState(s, err, ga.mode, now, ga.maxAge)
		ga.augment(&st, s, now)
		key := st.Signal + st.Headline + st.UpdatedAt + fmt.Sprint(st.Catalog)
		if key != lastKey || now.Sub(lastPush) >= 5*time.Second {
			ga.srv.PushState(st)
			lastKey, lastPush = key, now
		}
	}
}

// augment 는 카탈로그·코어 실행 상태를 State 에 얹는다 (§3.6, §4.3).
func (ga *guiApp) augment(st *State, s health.Status, now time.Time) {
	if ga.ring != nil {
		st.LogDropped = ga.ring.Dropped()
	}
	// 호스팅인데 코어가 죽어 있으면 최우선 오류
	if ga.mode == "hosting" && ga.core != nil && !ga.core.Running() {
		st.Signal = "red"
		st.CoreRunning = false
		st.Headline = "체크인 수집이 중지됨 — [수집 시작] 을 누르세요"
		if e := ga.core.LastErr(); e != "" {
			st.Headline = "코어 기동 실패 — " + e
		}
	}
	if ga.mgr == nil {
		return
	}
	cat, loadErr, pendingImport := ga.mgr.Snapshot()
	cv := &CatalogState{Loaded: cat != nil, Error: loadErr, PendingImport: pendingImport}
	ga.mu.Lock()
	cfg := ga.cfg
	ga.mu.Unlock()
	if cat != nil {
		cv.EventName = cat.EventName
		cv.ExportedAt = cat.ExportedAt
		cv.Stale = cat.Stale(now)
		// 리더별 세션 이름·검증·갱신 가능 배지
		for i := range st.Readers {
			r := &st.Readers[i]
			if r.SessionID == "" {
				continue
			}
			sess, ok := cat.Find(r.SessionID)
			if !ok {
				continue
			}
			r.SessionName = sess.Name
			r.SessionVerified = r.BoothName == sess.Name && r.UnitName == sess.UnitName &&
				(r.GateState == "ACTIVE" || r.GateState == "ACTIVE_WARNING")
			if cfg != nil {
				if cr, ok2 := cfg.Reader(r.ID); ok2 && cr.Token.Raw() != sess.Token {
					r.UpdateAvailable = true
					cv.UpdateAvailable = true
				}
			}
		}
	}
	st.Catalog = cv
	if st.Signal == "green" && (cv.UpdateAvailable || cv.PendingImport) {
		st.Signal = "yellow"
		if cv.PendingImport {
			st.Headline = "다운로드 폴더에서 새 세션 카탈로그 발견 — 세션 탭에서 가져오세요"
		} else {
			st.Headline = "카탈로그가 갱신됨 — 세션을 다시 선택하면 새 토큰이 적용됩니다"
		}
	}
}

func (ga *guiApp) reloadCfg() {
	if cfg, err := config.Load(ga.cfgPath); err == nil {
		ga.mu.Lock()
		ga.cfg = cfg
		ga.mu.Unlock()
	}
}

// ─── 세션 적용·재개 오케스트레이션 ───

type applyResult struct {
	NeedsServiceRestart bool   `json:"needsServiceRestart,omitempty"`
	Message             string `json:"message"`
}

// applySession 은 §3.4 시퀀스다: 중복 거부 → pending 차단 → 백업·쓰기 →
// (호스팅) 재기동, 실패 시 롤백.
func (ga *guiApp) applySession(readerID, sessionID string) (any, *apiError) {
	cat, _, _ := ga.mgr.Snapshot()
	if cat == nil {
		return nil, &apiError{"catalog_error", "카탈로그가 로드되지 않았습니다"}
	}
	sess, ok := cat.Find(sessionID)
	if !ok {
		return nil, &apiError{"not_found", "카탈로그에 없는 세션입니다"}
	}
	ga.mu.Lock()
	cfg := ga.cfg
	ga.mu.Unlock()
	if cfg == nil {
		return nil, &apiError{"invalid_request", "설정이 로드되지 않았습니다"}
	}
	if _, ok := cfg.Reader(readerID); !ok {
		return nil, &apiError{"not_found", "설정에 없는 리더입니다"}
	}
	for _, r := range cfg.Readers {
		if r.ID != readerID && (r.SessionID == sessionID || r.Token.Raw() == sess.Token) {
			return nil, &apiError{"conflict_busy",
				"한 세션(토큰)은 한 리더에만 지정할 수 있습니다 — 게이트가 더 필요하면 콘솔에서 토큰을 추가 발급해 카탈로그를 다시 내려받으세요"}
		}
	}
	// pending 차단 (§3.4) — status.json 의 관측값 사용
	if p := ga.readerPending(readerID); p > 0 {
		return nil, &apiError{"conflict_busy",
			fmt.Sprintf("이 리더에 미전송 %d건이 남아 있습니다 — 전송이 끝난 뒤 변경하거나, [재개] 화면에서 전송/폐기를 선택해 처리하세요", p)}
	}

	bak, err := WriteReaderSession(ga.cfgPath, readerID, sessionID, sess.Token)
	if err != nil {
		return nil, &apiError{"internal", err.Error()}
	}
	if ga.mode == "hosting" {
		if err := ga.core.Restart(context.Background()); err != nil {
			RollbackConfig(ga.cfgPath, bak)
			ga.core.Start(context.Background()) // 원설정으로 복구 기동
			return nil, &apiError{"core_restart_failed", "재기동 실패로 이전 설정으로 되돌렸습니다: " + err.Error()}
		}
	}
	RemoveBackup(bak)
	ga.reloadCfg()
	if ga.mode == "hosting" {
		return applyResult{Message: fmt.Sprintf("'%s' 세션을 적용했습니다 — 서버 확인 중", sess.Name)}, nil
	}
	return applyResult{NeedsServiceRestart: true,
		Message: fmt.Sprintf("'%s' 세션을 저장했습니다 — 서비스를 재시작해야 적용됩니다", sess.Name)}, nil
}

// resume 은 §3.6 재개 오케스트레이션이다: (선택) 새 토큰 반영 → 정지 →
// queue resume → 기동. 호스팅 모드 전용.
func (ga *guiApp) resume(readerID, pending, sessionID string) (any, *apiError) {
	if ga.mode != "hosting" {
		return nil, &apiError{"invalid_request",
			"관측 모드에서는 재개를 지원하지 않습니다 — 서비스 중지 후 cmd 에서 queue resume 을 실행하거나, 서비스를 중지하고 GUI 를 다시 열면 호스팅 모드로 재개할 수 있습니다"}
	}
	var bak string
	if sessionID != "" {
		cat, _, _ := ga.mgr.Snapshot()
		if cat == nil {
			return nil, &apiError{"catalog_error", "카탈로그가 로드되지 않았습니다"}
		}
		sess, ok := cat.Find(sessionID)
		if !ok {
			return nil, &apiError{"not_found", "카탈로그에 없는 세션입니다"}
		}
		var err error
		if bak, err = WriteReaderSession(ga.cfgPath, readerID, sessionID, sess.Token); err != nil {
			return nil, &apiError{"internal", err.Error()}
		}
	}
	if err := ga.core.Stop(); err != nil {
		if bak != "" {
			RollbackConfig(ga.cfgPath, bak)
		}
		return nil, &apiError{"internal", err.Error()}
	}
	cfg, err := config.Load(ga.cfgPath)
	if err == nil {
		_, err = app.QueueResume(cfg, readerID, pending)
	}
	if err != nil {
		if bak != "" {
			RollbackConfig(ga.cfgPath, bak)
		}
		ga.core.Start(context.Background())
		return nil, &apiError{"internal", "재개 실패: " + err.Error()}
	}
	if bak != "" {
		RemoveBackup(bak)
	}
	if err := ga.core.Start(context.Background()); err != nil {
		return nil, &apiError{"core_restart_failed", err.Error()}
	}
	ga.reloadCfg()
	return applyResult{Message: "재개했습니다 — 서버 확인 중"}, nil
}

func (ga *guiApp) coreControl(action string) *apiError {
	if ga.mode != "hosting" {
		return &apiError{"invalid_request", "호스팅 모드에서만 지원합니다"}
	}
	var err error
	switch action {
	case "stop":
		err = ga.core.Stop()
	case "start":
		err = ga.core.Start(context.Background())
	case "restart":
		err = ga.core.Restart(context.Background())
	default:
		return &apiError{"invalid_request", "action 은 start|stop|restart"}
	}
	if err != nil {
		return &apiError{"internal", err.Error()}
	}
	return nil
}

func (ga *guiApp) readerPending(readerID string) int64 {
	s, err := health.ReadStatus(filepath.Join(ga.dataDir, "status.json"))
	if err != nil {
		return 0
	}
	for _, r := range s.Readers {
		if r.ID == readerID {
			return r.Pending
		}
	}
	return 0
}

// catalogView 는 /api/catalog 응답이다 — 토큰 전문 없이 fingerprint 만 (§4.2).
func (ga *guiApp) catalogView() any {
	if ga.mgr == nil {
		return map[string]any{"loaded": false}
	}
	cat, loadErr, pendingImport := ga.mgr.Snapshot()
	ga.mu.Lock()
	cfg := ga.cfg
	ga.mu.Unlock()
	out := map[string]any{
		"loaded": cat != nil, "error": loadErr, "pendingImport": pendingImport,
		"path": ga.catalogPath(),
	}
	if cat == nil {
		return out
	}
	assigned := map[string]string{} // sessionId → readerId
	if cfg != nil {
		for _, r := range cfg.Readers {
			if r.SessionID != "" {
				assigned[r.SessionID] = r.ID
			}
		}
	}
	var sessions []map[string]any
	for _, s := range cat.Sessions {
		sessions = append(sessions, map[string]any{
			"id": s.ID, "name": s.Name, "unitName": s.UnitName,
			"tokenLabel": s.TokenLabel, "tokenFingerprint": s.TokenFP,
			"issuedAt": s.IssuedAt, "assignedReaderId": assigned[s.ID],
		})
	}
	out["eventName"] = cat.EventName
	out["exportedAt"] = cat.ExportedAt
	out["stale"] = cat.Stale(time.Now())
	out["warnings"] = cat.Warnings
	out["sessions"] = sessions
	return out
}

func (ga *guiApp) catalogOp(op string) *apiError {
	if ga.mgr == nil {
		return &apiError{"catalog_error", "설정이 없어 카탈로그를 쓸 수 없습니다"}
	}
	switch op {
	case "refresh":
		ga.mgr.reload(true)
		return nil
	case "import":
		if err := ga.mgr.Import(); err != nil {
			return &apiError{"catalog_error", "가져오기 실패: " + err.Error()}
		}
		return nil
	}
	return &apiError{"invalid_request", "지원하지 않는 동작"}
}

// CfgFingerprint 는 설정 지문이다 (GUI 설계 §1.1): 토큰 필드를 각 토큰
// fingerprint 로 치환한 정규화 JSON 의 SHA-256 앞 8자. 토큰 전문은 해시
// 입력에도 넣지 않는다.
func CfgFingerprint(cfg *config.Config) string {
	type reader struct {
		ID, Addr, TokenFP, SessionID string
	}
	canon := struct {
		APIHost, DataDir                                 string
		DebounceSec, QueueMaxAgeHours, RequestTimeoutSec int
		PowerGain, Buzzer                                int
		LogLevel, SessionsFile                           string
		Readers                                          []reader
	}{
		cfg.APIHost, cfg.DataDir,
		cfg.DebounceSec, cfg.QueueMaxAgeHours, cfg.RequestTimeoutSec,
		cfg.PowerGain, cfg.Buzzer, cfg.LogLevel, cfg.SessionsFile, nil,
	}
	for _, r := range cfg.Readers {
		canon.Readers = append(canon.Readers, reader{r.ID, r.Addr, r.Token.Fingerprint()[:8], r.SessionID})
	}
	b, _ := json.Marshal(canon)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}
