//go:build windows

// Package winsvc 는 Windows 서비스 수명주기 어댑터다 (설계서 §11).
package winsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "CongKongRFIDMiddleware"

// IsWindowsService 는 SCM 이 기동한 프로세스인지다.
func IsWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type appHandler struct {
	run func(ctx context.Context) error
}

func (h *appHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				changes <- svc.Status{State: svc.Stopped}
				return true, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				// Stop 수신 시 진행 중 트랜잭션을 최대 10초 기다린다 (설계서 §11.2).
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

// RunAsService 는 SCM 하에서 app 을 실행한다.
func RunAsService(run func(ctx context.Context) error) error {
	return svc.Run(ServiceName, &appHandler{run: run})
}

// Install 은 서비스를 수동 시작으로 등록한다(자동 시작 안 함) + 실패 복구.
// StartManual — 부팅 시 자동으로 켜지지 않는다. 최초 실행은 사람이 콘솔에서
// 수집을 켠다. 서비스는 운영자가(또는 GUI 가) 명시적으로 시작할 때만 돈다.
// 이미 설치돼 있으면 설정을 갱신한다(예: 이전 Automatic → Manual 로 교정).
func Install(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	absCfg, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM 연결 실패 (관리자 권한 필요): %w", err)
	}
	defer m.Disconnect()

	var s *mgr.Service
	if existing, oerr := m.OpenService(ServiceName); oerr == nil {
		// 기존 서비스 — 자동 시작 상태로 남아 있을 수 있으므로 Manual 로 갱신.
		s = existing
		cfg, cerr := s.Config()
		if cerr == nil {
			cfg.StartType = mgr.StartManual // 자동 시작 → 수동 으로 교정
			cfg.DelayedAutoStart = false
			if uerr := s.UpdateConfig(cfg); uerr != nil {
				s.Close()
				return fmt.Errorf("기존 서비스 갱신 실패: %w", uerr)
			}
		}
	} else {
		s, err = m.CreateService(ServiceName, exe, mgr.Config{
			DisplayName: "CongKong RFID Middleware",
			Description: "IDRO900EAE UHF RFID 태그를 CongKong 체크인 API 로 전달하는 미들웨어",
			StartType:   mgr.StartManual,
		}, "run", "--config", absCfg)
		if err != nil {
			return fmt.Errorf("서비스 생성 실패: %w", err)
		}
	}
	defer s.Close()

	// 실패 복구: 돌던 중 죽으면 1분 후 재시작 3회, 이후에도 재시작 유지.
	err = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Minute},
	}, 86400)
	if err != nil {
		return fmt.Errorf("복구 정책 설정 실패: %w", err)
	}
	return nil
}

func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM 연결 실패 (관리자 권한 필요): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("서비스 %s 없음", ServiceName)
	}
	defer s.Close()
	return s.Delete()
}

func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}

func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.Control(svc.Stop)
	return err
}

// SetAutoStart 는 무인 운영(부팅 시 자동 시작)을 켜거나 끈다 (관리자 권한).
// on: 서비스 없으면 설치 → Automatic(Delayed) + 지금 시작.
// off: Manual 로 되돌리고 중지. 서비스가 없으면 무시.
func SetAutoStart(configPath string, on bool) error {
	if !on {
		// 자동 시작 끄기 = Manual 로 등록/교정 + 중지
		if err := Install(configPath); err != nil {
			return err
		}
		_ = Stop()
		return nil
	}
	// 자동 시작 켜기
	if err := Install(configPath); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.StartType = mgr.StartAutomatic
	cfg.DelayedAutoStart = true
	if err := s.UpdateConfig(cfg); err != nil {
		return err
	}
	_ = s.Start() // 지금 바로 시작 (이미 실행 중이면 무시)
	return nil
}

// AutoStartInfo 는 GUI 표시용 서비스 상태다 (비관리자 조회 가능).
type AutoStartInfo struct {
	Installed bool
	AutoStart bool // Automatic 이면 true (무인 모드 켜짐)
	Running   bool
}

// QueryAutoStart 는 서비스 설치·자동시작·실행 여부를 조회한다 (권한 불필요).
func QueryAutoStart() AutoStartInfo {
	m, err := mgr.Connect()
	if err != nil {
		return AutoStartInfo{}
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return AutoStartInfo{Installed: false}
	}
	defer s.Close()
	info := AutoStartInfo{Installed: true}
	if cfg, cerr := s.Config(); cerr == nil {
		info.AutoStart = cfg.StartType == mgr.StartAutomatic
	}
	if st, serr := s.Query(); serr == nil {
		info.Running = st.State == svc.Running || st.State == svc.StartPending
	}
	return info
}
