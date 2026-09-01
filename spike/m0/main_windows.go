// M0 빌드 리스크 스파이크 (설계서 §9) — 일회용 검증 바이너리.
// 제품 코드가 아니다. 4개 항목의 실기기 성립 여부만 확인한다:
//  1. -H=windowsgui + AttachConsole 단일 exe (더블클릭=콘솔 없음, 인자 실행=콘솔 출력)
//  2. cgo-free 트레이 (fyne.io/systray, macOS 교차 빌드)
//  3. 127.0.0.1 HTTP + 기본 브라우저 자동 실행, 방화벽 프롬프트 무발생
//  4. KnownFolders 기반 Downloads 실제 경로 (OneDrive 리다이렉션 포함)
//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

//go:embed icon.ico
var iconICO []byte

const version = "spike-m0 v1 (2026-09-02)"

func main() {
	if len(os.Args) > 1 {
		runConsole()
		return
	}
	runGUI()
}

// ─── 항목 1: 콘솔 분기 ───

func attachParentConsole() bool {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	r, _, _ := k32.NewProc("AttachConsole").Call(uintptr(0xFFFFFFFF)) // ATTACH_PARENT_PROCESS
	if r == 0 {
		return false
	}
	// cmd 기본 코드페이지(CP949)에서 UTF-8 한글이 깨진다 — 콘솔을 UTF-8(65001)로 전환.
	// (스파이크 실기기 확인에서 발견 — 제품 CLI 분기에도 동일 적용 필요)
	k32.NewProc("SetConsoleOutputCP").Call(65001)
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
	return true
}

func downloadsPath() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil {
		return "(조회 실패: " + err.Error() + ")"
	}
	return p
}

func runConsole() {
	ok := attachParentConsole()
	status := "실패(부모 콘솔 없음)"
	if ok {
		status = "성공"
	}
	fmt.Println()
	fmt.Println("==", version, "— 콘솔 분기 확인 ==")
	fmt.Println("[항목 1] AttachConsole:", status)
	fmt.Println("[항목 4] Downloads 경로:", downloadsPath())
	fmt.Println("이 두 줄이 cmd 창에 보이면 항목 1·4 통과입니다.")
	fmt.Println("(프롬프트가 먼저 돌아온 뒤 출력이 찍혀도 정상입니다 — Enter 한 번)")
}

// ─── 항목 2·3·4: 트레이 + 로컬 HTTP + 브라우저 ───

const page = `<!doctype html><html lang="ko"><head><meta charset="utf-8">
<title>M0 스파이크</title>
<style>body{font-family:'Malgun Gothic',sans-serif;max-width:640px;margin:40px auto;padding:0 16px}
table{border-collapse:collapse;width:100%%}td,th{border:1px solid #ccc;padding:8px;text-align:left}
th{background:#f5f5f5}code{background:#eee;padding:2px 4px}</style></head><body>
<h2>🟠 M0 스파이크 — %s</h2>
<p>이 페이지가 <b>자동으로</b> 열렸고 <b>방화벽 경고가 뜨지 않았다면</b> 항목 3 통과입니다.</p>
<table>
<tr><th>#</th><th>확인 항목</th><th>확인 방법</th></tr>
<tr><td>1</td><td>windowsgui 단일 exe</td><td>더블클릭 때 검은 cmd 창이 <b>안 떴는가</b>?<br>
cmd 에서 <code>spike-m0.exe check</code> 실행 시 출력이 보이는가?</td></tr>
<tr><td>2</td><td>트레이 아이콘</td><td>작업 표시줄 트레이(우하단 ^)에 주황 원 아이콘이 있고,
우클릭 메뉴 [화면 열기]/[종료]가 동작하는가?</td></tr>
<tr><td>3</td><td>로컬 HTTP + 브라우저</td><td>이 페이지가 자동으로 열렸는가? 방화벽 프롬프트가 없었는가?<br>
주소: <code>%s</code></td></tr>
<tr><td>4</td><td>Downloads 실제 경로</td><td><code>%s</code><br>
탐색기의 "다운로드" 폴더와 같은 곳인가? (OneDrive 사용 노트북이면 특히 확인)</td></tr>
</table>
<p>페이지 생성: %s · 새로고침하면 시각이 바뀌어야 서버 생존 확인</p>
<p><b>4개 결과(각각 O/X)를 개발자에게 알려주세요.</b> 종료는 트레이 우클릭 → 종료.</p>
</body></html>`

func runGUI() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, page, version, url, downloadsPath(), time.Now().Format("15:04:05"))
	})
	go func() { _ = http.Serve(ln, nil) }()

	openBrowser := func() { _ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() }
	go func() { time.Sleep(300 * time.Millisecond); openBrowser() }()

	systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTooltip("M0 스파이크 — " + url)
		open := systray.AddMenuItem("화면 열기", "확인 페이지를 브라우저로 연다")
		systray.AddSeparator()
		quit := systray.AddMenuItem("종료", "스파이크 종료")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					openBrowser()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() { os.Exit(0) })
}
