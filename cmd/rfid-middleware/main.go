// rfid-middleware — IDRO900EAE UHF RFID 리더가 읽은 태그를 CongKong 서버
// 체크인으로 전달하는 Windows 상주 미들웨어.
//
// 계획서: docs/features/pulse/rfid-middleware-idro900eae-development-plan.ko.md
// 설계서: docs/02-design/features/rfid-middleware-idro900eae.design.md
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/app"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/gui"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/winsvc"
)

var version = "dev" // -ldflags "-X main.version=..." 로 주입

const usage = `rfid-middleware — IDRO900EAE → CongKong 체크인 미들웨어

사용법:
  rfid-middleware                 # 인자 없음 = GUI (트레이 + 브라우저 상태 화면)
  rfid-middleware gui [--config <path>]
  rfid-middleware run --config <path>
  rfid-middleware replay --stdin --reader <id> --config <path> [--drain]
  rfid-middleware replay --file <fixture> [--ndjson] --reader <id> --config <path> [--drain]
  rfid-middleware validate-config --config <path>
  rfid-middleware status --data-dir <path>
  rfid-middleware queue resume --reader <id> --pending send|discard --config <path>
  rfid-middleware service install|uninstall|start|stop --config <path>
  rfid-middleware version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		// 인자 없음 = GUI (더블클릭 기동 — GUI 설계 §7.1)
		return cmdGUI(nil)
	}
	// 인자 있음 = CLI. windowsgui 빌드에서는 부모 콘솔에 붙는다 (M0 확인).
	gui.AttachConsole()
	switch args[0] {
	case "gui":
		return cmdGUI(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "replay":
		return cmdReplay(args[1:])
	case "validate-config":
		return cmdValidate(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "queue":
		return cmdQueue(args[1:])
	case "service":
		return cmdService(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("알 수 없는 명령: %s", args[0])
	}
}

func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		return nil, fmt.Errorf("--config 가 필요합니다")
	}
	return config.Load(path)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}

	if winsvc.IsWindowsService() {
		// Windows Service — echo 없이, SCM 수명주기로 실행 (설계서 §11.2)
		return winsvc.RunAsService(func(ctx context.Context) error {
			return app.Run(ctx, app.Options{Cfg: cfg, Version: version, Mode: "service"})
		})
	}
	ctx, cancel := signalContext()
	defer cancel()
	return app.Run(ctx, app.Options{Cfg: cfg, Echo: os.Stdout, Version: version, Mode: "cli"})
}

func cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로 (기본: exe 옆 config.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *cfgPath
	if path == "" {
		path = defaultGUIConfig()
	}
	ctx, cancel := signalContext()
	defer cancel()
	return gui.Run(ctx, gui.Options{ConfigPath: path, Version: version})
}

// defaultGUIConfig 는 --config 미지정 시 설정 파일을 찾는다:
// ① exe 옆 config.json(무설치·이식형) → ② 설치 위치
// %ProgramData%\CongKong\RFIDMiddleware\config.json → ③ 없으면 exe 옆(온보딩용).
// 설치된 exe 를 바탕화면 바로가기가 아니라 직접 더블클릭해도 ProgramData 의
// 설정을 찾게 한다 (Program Files 는 쓰기 불가라 exe 옆엔 config 가 없다).
func defaultGUIConfig() string {
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	local := filepath.Join(exeDir, "config.json")
	if fileExists(local) {
		return local
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		installed := filepath.Join(pd, "CongKong", "RFIDMiddleware", "config.json")
		if fileExists(installed) {
			return installed
		}
	}
	return local
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로")
	useStdin := fs.Bool("stdin", false, "stdin 재생")
	file := fs.String("file", "", "fixture 파일 재생")
	ndjson := fs.Bool("ndjson", false, "chunk/시각 확장 NDJSON fixture 형식")
	readerID := fs.String("reader", "", "재생 대상 reader id")
	drain := fs.Bool("drain", true, "입력 소진 + 큐 드레인 후 종료")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *readerID == "" {
		return fmt.Errorf("--reader 가 필요합니다")
	}
	if _, ok := cfg.Reader(*readerID); !ok {
		return fmt.Errorf("reader %q 가 설정에 없습니다", *readerID)
	}

	var in *os.File
	switch {
	case *useStdin && *file != "":
		return fmt.Errorf("--stdin 과 --file 은 함께 쓸 수 없습니다")
	case *useStdin:
		in = os.Stdin
	case *file != "":
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	default:
		return fmt.Errorf("--stdin 또는 --file 이 필요합니다")
	}

	ctx, cancel := signalContext()
	defer cancel()
	return app.Run(ctx, app.Options{
		Cfg: cfg, Echo: os.Stdout,
		ReplayInput: in, ReplayNDJSON: *ndjson, ReplayReader: *readerID,
		DrainAndExit: *drain,
	})
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate-config", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("설정 유효: reader %d개, apiHost %s\n", len(cfg.Readers), cfg.APIHost)
	for _, r := range cfg.Readers {
		// 토큰은 fingerprint 만 출력한다.
		fmt.Printf("  - %s %s (token fp %s)\n", r.ID, r.Addr, r.Token.Fingerprint()[:8])
	}
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "데이터 디렉토리")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return fmt.Errorf("--data-dir 가 필요합니다")
	}
	s, err := health.ReadStatus(filepath.Join(*dataDir, "status.json"))
	if err != nil {
		return fmt.Errorf("status.json 읽기 실패 (서비스가 실행 중인지 확인): %w", err)
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdQueue(args []string) error {
	if len(args) < 1 || args[0] != "resume" {
		return fmt.Errorf("사용법: rfid-middleware queue resume --reader <id> --pending send|discard --config <path>")
	}
	fs := flag.NewFlagSet("queue resume", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로")
	readerID := fs.String("reader", "", "재개할 reader id")
	pending := fs.String("pending", "", "보존 행 처리: send | discard")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *readerID == "" {
		return fmt.Errorf("--reader 가 필요합니다")
	}
	msg, err := app.QueueResume(cfg, *readerID, *pending)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func cmdService(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("사용법: rfid-middleware service install|uninstall|start|stop --config <path>")
	}
	sub := args[0]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "설정 파일 경로")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch sub {
	case "install":
		// 설치 전 설정을 검증한다 — 잘못된 설정으로 서비스가 crash loop 에 빠지지 않게.
		if _, err := loadConfig(*cfgPath); err != nil {
			return err
		}
		if err := winsvc.Install(*cfgPath); err != nil {
			return err
		}
		fmt.Printf("서비스 %s 등록 완료 (수동 시작 — 부팅 자동 시작 안 함)\n", winsvc.ServiceName)
		return nil
	case "reset":
		// 남은 서비스를 정지 + 수동(Manual)으로 되돌린다. 서비스가 없으면 no-op.
		// 설치 시 항상 호출해 예전 Automatic 서비스의 부팅 자동 수집을 없앤다.
		if err := winsvc.Reset(); err != nil {
			return err
		}
		fmt.Println("서비스 상태 정리 완료 (실행 중이면 정지, 수동 시작으로 설정)")
		return nil
	case "uninstall":
		return winsvc.Uninstall()
	case "start":
		return winsvc.Start()
	case "stop":
		return winsvc.Stop()
	case "auto":
		// service auto on|off --config <path> — 무인 운영(부팅 시 자동 시작) 켜기/끄기.
		// "on"/"off" 가 비-플래그라 위 fs 파싱이 그 앞에서 멈춘다 — 여기서 자체 파싱.
		rest := fs.Args() // 예: ["on", "--config", "path"]
		if len(rest) < 1 || (rest[0] != "on" && rest[0] != "off") {
			return fmt.Errorf("사용법: rfid-middleware service auto on|off --config <path>")
		}
		on := rest[0] == "on"
		af := flag.NewFlagSet("service auto", flag.ContinueOnError)
		acfg := af.String("config", "", "설정 파일 경로")
		if err := af.Parse(rest[1:]); err != nil {
			return err
		}
		if err := winsvc.SetAutoStart(*acfg, on); err != nil {
			return err
		}
		fmt.Printf("무인 자동 시작: %v\n", on)
		return nil
	default:
		return fmt.Errorf("알 수 없는 service 명령: %s", sub)
	}
}
