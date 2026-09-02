package gui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/app"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
)

const coreStopGrace = 10 * time.Second // 서비스와 동일 (GUI 설계 §7.3)

// CoreController 는 호스팅 모드에서 app.Run 의 수명주기를 소유한다
// (GUI 설계 §1, §6.5). 잠금은 항상 GUI 가 선획득해 위임한다.
type CoreController struct {
	CfgPath string
	Version string
	Ring    *LogRing

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan error
	running  bool
	intent   bool // 운영자가 수집을 켠 상태(대기 vs 실행 구분)
	crashed  bool // intent=true 인데 프로세스가 비정상 종료
	lastErr  string
	cfg      *config.Config
}

// Start 는 설정을 다시 읽고 잠금을 획득해 코어를 기동한다.
func (c *CoreController) Start(parent context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return fmt.Errorf("이미 실행 중")
	}
	cfg, err := config.Load(c.CfgPath)
	if err != nil {
		return fmt.Errorf("설정 로드 실패: %w", err)
	}
	release, err := app.AcquireLock(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("다른 인스턴스(서비스?)가 실행 중: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, app.Options{
			Cfg: cfg, Echo: c.Ring,
			Version: c.Version, Mode: "hosting",
			PreacquiredLock: release, // app.Run 이 종료 시 release 한다 (§6.5)
		})
	}()
	c.cancel, c.done, c.running, c.cfg, c.lastErr = cancel, done, true, cfg, ""
	c.intent, c.crashed = true, false

	// 비정상 종료 감시 — running 플래그를 정리해 UI 가 알 수 있게
	go func() {
		err := <-done
		c.mu.Lock()
		c.running = false
		if err != nil && ctx.Err() == nil {
			c.lastErr = err.Error()
			c.crashed = true // intent=true 상태에서 죽음 = 크래시
		}
		c.mu.Unlock()
	}()
	return nil
}

// Stop 은 grace 안에 코어를 정리한다. 이미 멈췄으면 no-op.
func (c *CoreController) Stop() error {
	c.mu.Lock()
	c.intent = false // 운영자 의도: 중지 (대기 상태)
	c.crashed = false
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	c.mu.Unlock()

	cancel()
	deadline := time.After(coreStopGrace)
	for {
		c.mu.Lock()
		running := c.running
		c.mu.Unlock()
		if !running {
			return nil
		}
		select {
		case <-deadline:
			return fmt.Errorf("코어가 %s 안에 종료되지 않음", coreStopGrace)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *CoreController) Restart(parent context.Context) error {
	if err := c.Stop(); err != nil {
		return err
	}
	return c.Start(parent)
}

func (c *CoreController) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Standby 는 운영자가 아직 수집을 켜지 않았거나 중지한 상태다(크래시 아님).
func (c *CoreController) Standby() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.running && !c.intent
}

// Crashed 는 수집 중이던 코어가 비정상 종료한 상태다.
func (c *CoreController) Crashed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.crashed
}

func (c *CoreController) LastErr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Config 는 마지막으로 기동에 쓴 설정이다 (세션 검증·중복 검사용).
func (c *CoreController) Config() *config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}
