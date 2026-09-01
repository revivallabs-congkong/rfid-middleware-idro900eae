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

// Install 은 서비스를 등록한다: Automatic (Delayed Start) + 실패 복구 재시작.
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

	if s, err := m.OpenService(ServiceName); err == nil {
		s.Close()
		return fmt.Errorf("서비스 %s 가 이미 설치돼 있음", ServiceName)
	}

	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		DisplayName:      "CongKong RFID Middleware",
		Description:      "IDRO900EAE UHF RFID 태그를 CongKong 체크인 API 로 전달하는 미들웨어",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
	}, "run", "--config", absCfg)
	if err != nil {
		return fmt.Errorf("서비스 생성 실패: %w", err)
	}
	defer s.Close()

	// 실패 복구: 1분 후 재시작 3회, 이후에도 재시작 유지 (설계서 §11.2).
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
