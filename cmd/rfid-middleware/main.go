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
		exe, err := os.Executable()
		if err == nil {
			path = filepath.Join(filepath.Dir(exe), "config.json")
		} else {
			path = "config.json"
		}
	}
	ctx, cancel := signalContext()
	defer cancel()
	return gui.Run(ctx, gui.Options{ConfigPath: path, Version: version})
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
		fmt.Printf("서비스 %s 설치 완료 (Automatic, Delayed Start)\n", winsvc.ServiceName)
		return nil
	case "uninstall":
		return winsvc.Uninstall()
	case "start":
		return winsvc.Start()
	case "stop":
		return winsvc.Stop()
	default:
		return fmt.Errorf("알 수 없는 service 명령: %s", sub)
	}
}
