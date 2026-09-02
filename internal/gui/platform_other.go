//go:build !windows

package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// 비 Windows 는 개발용 — 트레이 없이 ctx 종료까지 대기한다.

func OpenBrowser(url string) {
	_ = exec.Command("open", url).Start()
}

func runTray(ctx context.Context, cancel context.CancelFunc, url string, confirmQuit func() bool) {
	fmt.Println("GUI:", url, "(Ctrl+C 로 종료)")
	<-ctx.Done()
	cancel()
}

// confirmQuit — 비 Windows 개발 환경은 항상 허용.
func confirmQuit() bool { return true }

// downloadsDir — 개발용 ~/Downloads.
func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/Downloads"
}

func serviceControl(configPath string) func(action string) error {
	return func(action string) error {
		return fmt.Errorf("windows 전용 기능입니다")
	}
}

// AttachConsole 은 Windows 전용 — no-op.
func AttachConsole() {}
