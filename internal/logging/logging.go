// Package logging 은 JSON Lines 구조화 로그를 담당한다 (설계서 §12.2).
// 서버 response body·attendee·이름·전화·이메일·전체 토큰은 필드 자체를 만들지 않는다.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "?"
}

func ParseLevel(s string) Level {
	switch s {
	case "debug":
		return Debug
	case "warn":
		return Warn
	case "error":
		return Error
	default:
		return Info
	}
}

// F 는 이벤트 필드다. 허용 필드만 쓴다 — 금지 키는 write 시점에 차단한다.
type F map[string]any

// forbiddenKeys 는 어떤 경로로도 로그에 나가면 안 되는 필드명이다 (R8).
var forbiddenKeys = map[string]bool{
	"attendee": true, "fullName": true, "firstName": true, "lastName": true,
	"mobileNumber": true, "emailAddress": true, "membershipName": true,
	"membershipPosition": true, "token": true, "pulseToken": true, "body": true,
}

const (
	rotateSize = 10 * 1024 * 1024 // 10 MiB
	keepFiles  = 10
	keepDays   = 14
)

// Logger 는 파일 회전 + 선택적 콘솔 echo 로거다.
type Logger struct {
	mu      sync.Mutex
	dir     string
	level   Level
	echo    io.Writer // foreground 실행 시 os.Stdout
	f       *os.File
	fDate   string
	fSize   int64
	nowFunc func() time.Time
}

// New 는 dir 에 middleware-YYYYMMDD.jsonl 로그를 연다. dir 가 비면 echo 전용이다.
func New(dir string, level Level, echo io.Writer) (*Logger, error) {
	l := &Logger{dir: dir, level: level, echo: echo, nowFunc: time.Now}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("로그 디렉토리 생성 실패: %w", err)
		}
	}
	return l, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		return l.f.Close()
	}
	return nil
}

func (l *Logger) Log(level Level, event string, fields F) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	rec := map[string]any{
		"ts":    l.nowFunc().Format(time.RFC3339Nano),
		"level": level.String(),
		"event": event,
	}
	for k, v := range fields {
		if forbiddenKeys[k] {
			continue // 금지 필드는 조용히 버리지 않고 아래에 표시를 남긴다
		}
		rec[k] = v
	}
	for k := range fields {
		if forbiddenKeys[k] {
			rec["redactedField"] = k
			break
		}
	}

	b, err := json.Marshal(rec)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"ts":%q,"level":"error","event":"LOG_MARSHAL_FAILED","origEvent":%q}`,
			l.nowFunc().Format(time.RFC3339Nano), event))
	}
	line := append(b, '\n')

	if l.echo != nil {
		l.echo.Write(line)
	}
	if l.dir == "" {
		return
	}
	if err := l.writeFile(line); err != nil && l.echo == nil {
		// logging 실패는 stderr 로 최소 fallback. DB queue 가 우선이다 (설계서 §12.2).
		fmt.Fprintf(os.Stderr, "log write failed: %v\n", err)
	}
}

func (l *Logger) writeFile(line []byte) error {
	date := l.nowFunc().Format("20060102")
	if l.f == nil || l.fDate != date || l.fSize+int64(len(line)) > rotateSize {
		if l.f != nil {
			l.f.Close()
			l.f = nil
		}
		if err := l.openFile(date); err != nil {
			return err
		}
		l.cleanup()
	}
	n, err := l.f.Write(line)
	l.fSize += int64(n)
	return err
}

func (l *Logger) openFile(date string) error {
	// 같은 날 회전 시 -N 접미를 올린다.
	base := filepath.Join(l.dir, "middleware-"+date)
	path := base + ".jsonl"
	for i := 1; ; i++ {
		st, err := os.Stat(path)
		if os.IsNotExist(err) || (err == nil && st.Size() < rotateSize) {
			break
		}
		path = fmt.Sprintf("%s.%d.jsonl", base, i)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	l.f = f
	l.fDate = date
	if st != nil {
		l.fSize = st.Size()
	} else {
		l.fSize = 0
	}
	return nil
}

// cleanup 은 보관 상한(파일 수·일수)을 적용한다. 실패해도 무시한다.
func (l *Logger) cleanup() {
	entries, err := filepath.Glob(filepath.Join(l.dir, "middleware-*.jsonl"))
	if err != nil {
		return
	}
	sort.Strings(entries)
	cutoff := l.nowFunc().AddDate(0, 0, -keepDays).Format("20060102")
	var candidates []string
	for _, e := range entries {
		name := filepath.Base(e)
		datePart := strings.TrimPrefix(name, "middleware-")
		if len(datePart) >= 8 && datePart[:8] < cutoff {
			os.Remove(e)
			continue
		}
		candidates = append(candidates, e)
	}
	if n := len(candidates) - keepFiles; n > 0 {
		for _, e := range candidates[:n] {
			os.Remove(e)
		}
	}
}

func (l *Logger) Debugf(event string, f F) { l.Log(Debug, event, f) }
func (l *Logger) Infof(event string, f F)  { l.Log(Info, event, f) }
func (l *Logger) Warnf(event string, f F)  { l.Log(Warn, event, f) }
func (l *Logger) Errorf(event string, f F) { l.Log(Error, event, f) }
