//go:build windows

package gui

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

//go:embed assets/icon.ico
var iconICO []byte

// OpenBrowser 는 기본 브라우저로 URL 을 연다 (M0 검증 방식 — rundll32).
func OpenBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// runTray 는 트레이 상주 셸이다 (GUI 설계 §7.2). 종료 시 cancel 을 부른다.
func runTray(ctx context.Context, cancel context.CancelFunc, url string) {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTooltip("CongKong RFID 미들웨어")
		open := systray.AddMenuItem("화면 열기", "상태 화면을 브라우저로 연다")
		systray.AddSeparator()
		quit := systray.AddMenuItem("종료", "GUI 를 종료한다 (서비스는 계속 동작)")
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-open.ClickedCh:
					OpenBrowser(url)
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() { cancel() })
}

// serviceControl 은 자기 exe 를 관리자 권한으로 재실행해 서비스를 제어한다
// (GUI 설계 §4.2 — 코어에 IPC 를 만들지 않는 원칙).
func serviceControl(configPath string) func(action string) error {
	return func(action string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		args := fmt.Sprintf(`service %s --config "%s"`, action, configPath)
		exePtr, _ := windows.UTF16PtrFromString(exe)
		argPtr, _ := windows.UTF16PtrFromString(args)
		verb, _ := windows.UTF16PtrFromString("runas")
		return windows.ShellExecute(0, verb, exePtr, argPtr, nil, windows.SW_HIDE)
	}
}

// AttachConsole 은 부모 콘솔에 붙고 UTF-8 로 전환한다 (M0 확인 사항 —
// windowsgui 빌드의 CLI 분기, GUI 설계 §7.1).
func AttachConsole() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	r, _, _ := k32.NewProc("AttachConsole").Call(uintptr(0xFFFFFFFF)) // ATTACH_PARENT_PROCESS
	if r == 0 {
		return
	}
	k32.NewProc("SetConsoleOutputCP").Call(65001)
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
}
