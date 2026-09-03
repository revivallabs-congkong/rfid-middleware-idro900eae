//go:build windows

package gui

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/winsvc"
)

//go:embed assets/icon.ico
var iconICO []byte

//go:embed assets/icon-green.ico
var iconGreen []byte

//go:embed assets/icon-yellow.ico
var iconYellow []byte

//go:embed assets/icon-red.ico
var iconRed []byte

//go:embed assets/icon-gray.ico
var iconGray []byte

// TrayState 는 트레이 아이콘·툴팁 갱신 신호다.
type TrayState struct {
	Signal     string // green|yellow|red|gray
	Collecting bool
	Network    string // online|offline|unknown
}

func trayIcon(signal string) []byte {
	switch signal {
	case "green":
		return iconGreen
	case "yellow":
		return iconYellow
	case "red":
		return iconRed
	default:
		return iconGray
	}
}

func trayTip(ts TrayState) string {
	coll := "수집 중지"
	if ts.Collecting {
		coll = "수집 중"
	}
	net := ""
	switch ts.Network {
	case "online":
		net = " · 인터넷 연결됨"
	case "offline":
		net = " · 인터넷 끊김"
	}
	return "CongKong RFID — " + coll + net
}

// OpenBrowser 는 기본 브라우저로 URL 을 연다 (M0 검증 방식 — rundll32).
func OpenBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// runTray 는 트레이 상주 셸이다 (GUI 설계 §7.2). confirmQuit 이 false 를
// 돌려주면 종료를 취소한다 (호스팅 모드 태그 유실 방지 — §7.3).
func runTray(ctx context.Context, cancel context.CancelFunc, url string, confirmQuit func() bool, trayCh <-chan TrayState) {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() {
		systray.SetIcon(iconGray)
		systray.SetTooltip("CongKong RFID 미들웨어")
		status := systray.AddMenuItem("상태: 시작 중…", "")
		status.Disable()
		open := systray.AddMenuItem("화면 열기", "상태 화면을 브라우저로 연다")
		systray.AddSeparator()
		quit := systray.AddMenuItem("종료", "프로그램을 종료한다")
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case ts := <-trayCh:
					systray.SetIcon(trayIcon(ts.Signal))
					tip := trayTip(ts)
					systray.SetTooltip(tip)
					status.SetTitle("상태: " + strings.TrimPrefix(tip, "CongKong RFID — "))
				case <-open.ClickedCh:
					OpenBrowser(url)
				case <-quit.ClickedCh:
					if confirmQuit() {
						systray.Quit()
						return
					}
				}
			}
		}()
	}, func() { cancel() })
}

// confirmQuit 은 호스팅 모드 종료 확인 다이얼로그다 (§7.3).
func confirmQuit() bool {
	title, _ := windows.UTF16PtrFromString("종료 확인")
	text, _ := windows.UTF16PtrFromString(
		"지금 종료하면 체크인 수집이 중단됩니다.\n리더가 읽는 태그가 기록되지 않습니다.\n\n정말 종료할까요?")
	const MB_YESNO, MB_ICONWARNING, MB_DEFBUTTON2, IDYES = 0x4, 0x30, 0x100, 6
	r, _ := windows.MessageBox(0, text, title, MB_YESNO|MB_ICONWARNING|MB_DEFBUTTON2)
	return r == IDYES
}

// downloadsDir 는 실제 다운로드 폴더다 (KnownFolders — OneDrive 리다이렉션
// 대응, M0 확인 항목).
func downloadsDir() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0); err == nil && p != "" {
		return p
	}
	// 폴백 — KnownFolders 실패 시 %USERPROFILE%\Downloads
	if up := os.Getenv("USERPROFILE"); up != "" {
		return up + `\Downloads`
	}
	return ""
}

// serviceControl 은 자기 exe 를 관리자 권한으로 재실행해 서비스를 제어한다
// (GUI 설계 §4.2 — 코어에 IPC 를 만들지 않는 원칙).
func serviceControl(configPath string) func(action string) error {
	return func(action string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		// action → service 서브커맨드. auto-on/auto-off 는 무인 자동 시작 토글.
		var sub string
		switch action {
		case "auto-on":
			sub = "auto on"
		case "auto-off":
			sub = "auto off"
		default:
			sub = action // start | stop
		}
		args := fmt.Sprintf(`service %s --config "%s"`, sub, configPath)
		exePtr, _ := windows.UTF16PtrFromString(exe)
		argPtr, _ := windows.UTF16PtrFromString(args)
		verb, _ := windows.UTF16PtrFromString("runas")
		return windows.ShellExecute(0, verb, exePtr, argPtr, nil, windows.SW_HIDE)
	}
}

// networkSetup 은 자기 exe 를 관리자 권한으로 재실행해 어댑터에 고정 IP 를
// 부여한다 (netsh 는 관리자 필요 — serviceControl 과 같은 재실행 패턴).
func networkSetup(iface, ip, mask string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := fmt.Sprintf(`net setup --iface "%s" --ip %s --mask %s`, iface, ip, mask)
	exePtr, _ := windows.UTF16PtrFromString(exe)
	argPtr, _ := windows.UTF16PtrFromString(args)
	verb, _ := windows.UTF16PtrFromString("runas")
	return windows.ShellExecute(0, verb, exePtr, argPtr, nil, windows.SW_HIDE)
}

// autoStartInfo 는 무인 모드 상태를 GUI 로 돌려준다 (비관리자 조회).
func autoStartInfo() (installed, auto, running bool) {
	i := winsvc.QueryAutoStart()
	return i.Installed, i.AutoStart, i.Running
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
