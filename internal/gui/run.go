package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
)

type Options struct {
	ConfigPath string
	Version    string
}

// Run 은 GUI 를 기동한다. M1: 항상 관측 모드 — 코어(서비스/CLI)는 별도
// 프로세스로 돌고, GUI 는 status.json·로그를 읽는다. (호스팅 모드는 M2)
func Run(ctx context.Context, opts Options) error {
	meta := Meta{Version: opts.Version, Mode: "observer", ConfigPath: opts.ConfigPath}

	var dataDir string
	var maxAge int
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		// 설정 없음/오류 — 안내 화면만 제공 (M1 최소 온보딩)
		meta.ConfigError = err.Error()
	} else {
		dataDir = cfg.DataDir
		maxAge = cfg.QueueMaxAgeHours
		meta.DataDir = cfg.DataDir
		meta.CfgFingerprint = CfgFingerprint(cfg)
	}

	srv, err := NewServer(meta)
	if err != nil {
		return fmt.Errorf("로컬 HTTP 기동 실패: %w", err)
	}
	srv.ServiceControl = serviceControl(opts.ConfigPath)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if dataDir != "" {
		obs := &Observer{DataDir: dataDir, Mode: "observer", QueueMaxAgeHours: maxAge, Server: srv}
		go obs.Run(runCtx)
	}
	go srv.Serve()
	OpenBrowser(srv.URL())

	// 트레이가 수명을 소유한다 (GUI 설계 §7.3). 트레이 미지원 환경은 ctx 대기.
	runTray(runCtx, cancel, srv.URL())
	return nil
}

// CfgFingerprint 는 설정 지문이다 (GUI 설계 §1.1): 토큰 필드를 각 토큰
// fingerprint 로 치환한 정규화 JSON 의 SHA-256 앞 8자. 토큰 전문은 해시
// 입력에도 넣지 않는다.
func CfgFingerprint(cfg *config.Config) string {
	type reader struct {
		ID, Addr, TokenFP, SessionID string
	}
	canon := struct {
		APIHost, DataDir                                          string
		DebounceSec, QueueMaxAgeHours, RequestTimeoutSec          int
		PowerGain, Buzzer                                         int
		LogLevel, SessionsFile                                    string
		Readers                                                   []reader
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
