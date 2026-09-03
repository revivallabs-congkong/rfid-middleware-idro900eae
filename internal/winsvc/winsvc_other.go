//go:build !windows

// Package winsvc 는 Windows 서비스 수명주기 어댑터다 (설계서 §11).
// 다른 OS(개발 환경)에서는 stub 이다.
package winsvc

import (
	"context"
	"errors"
)

const ServiceName = "CongKongRFIDMiddleware"

var errWindowsOnly = errors.New("windows 전용 명령입니다")

func IsWindowsService() bool { return false }

func RunAsService(run func(ctx context.Context) error) error { return errWindowsOnly }

func Install(configPath string) error { return errWindowsOnly }
func Uninstall() error                { return errWindowsOnly }
func Start() error                    { return errWindowsOnly }
func Stop() error                     { return errWindowsOnly }

type AutoStartInfo struct {
	Installed bool
	AutoStart bool
	Running   bool
}

func SetAutoStart(configPath string, on bool) error { return errWindowsOnly }
func QueryAutoStart() AutoStartInfo                 { return AutoStartInfo{} }
