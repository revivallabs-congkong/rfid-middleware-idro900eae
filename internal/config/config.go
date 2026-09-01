// Package config 는 엄격한 JSON 설정 로드·검증·마스킹을 담당한다 (설계서 §9).
// 오류 문자열에는 토큰 원문이나 토큰이 들어간 원문 JSON 을 절대 포함하지 않는다.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

const (
	MaxReaders = 8
)

var readerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// wire 는 파일의 JSON 형태 그대로다. 검증 후 Config 로 바꾸며,
// 토큰 원문 문자열은 Config 에 남기지 않는다.
type wire struct {
	Version           *int         `json:"version"`
	APIHost           string       `json:"apiHost"`
	DataDir           string       `json:"dataDir"`
	DebounceSec       *int         `json:"debounceSec"`
	QueueMaxAgeHours  *int         `json:"queueMaxAgeHours"`
	RequestTimeoutSec *int         `json:"requestTimeoutSec"`
	PowerGain         *int         `json:"powerGain"`
	Buzzer            *int         `json:"buzzer"`
	LogLevel          string       `json:"logLevel"`
	Readers           []wireReader `json:"readers"`
}

type wireReader struct {
	ID         string `json:"id"`
	Addr       string `json:"addr"`
	PulseToken string `json:"pulseToken"`
}

// Reader 는 검증이 끝난 리더 설정 1개다.
type Reader struct {
	ID    string
	Addr  string
	Token domain.Secret
}

// Config 는 검증이 끝난 실행 설정이다.
type Config struct {
	APIHost           string
	DataDir           string
	DebounceSec       int
	QueueMaxAgeHours  int
	RequestTimeoutSec int
	PowerGain         int
	Buzzer            int
	LogLevel          string
	Readers           []Reader
}

// Load 는 파일을 읽어 검증까지 마친 Config 를 돌려준다.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("설정 파일을 열 수 없음: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Parse 는 JSON 스트림을 파싱·검증한다. 알 수 없는 필드는 오타 방지를 위해 오류다.
func Parse(r interface{ Read([]byte) (int, error) }) (*Config, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		// 디코더 오류에 원문 조각이 섞이지 않도록 위치 정보만 전달한다.
		return nil, fmt.Errorf("설정 JSON 파싱 실패 (주석·trailing comma·알 수 없는 필드 금지): %s", redactDecodeErr(err))
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil || !isEOF(err) {
		return nil, fmt.Errorf("설정 파일에 JSON 문서가 하나를 초과함")
	}
	return validate(&w)
}

func isEOF(err error) bool { return err != nil && err.Error() == "EOF" }

// redactDecodeErr 는 json 오류 문자열에서 값 원문이 노출될 수 있는 부분을 남기지 않는다.
// Go 표준 디코더 오류는 값 원문을 포함하지 않지만(필드명·타입·오프셋만),
// 방어적으로 pulseToken 관련 오류는 필드명만 남긴다.
func redactDecodeErr(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "pulseToken") {
		return "pulseToken 필드 형식 오류"
	}
	return msg
}

func validate(w *wire) (*Config, error) {
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if w.Version == nil || *w.Version != 1 {
		fail("version: 1 이어야 함")
	}

	cfg := &Config{
		APIHost:           w.APIHost,
		DataDir:           w.DataDir,
		DebounceSec:       60,
		QueueMaxAgeHours:  24,
		RequestTimeoutSec: 10,
		PowerGain:         300,
		Buzzer:            0,
		LogLevel:          "info",
	}

	if w.DebounceSec != nil {
		cfg.DebounceSec = *w.DebounceSec
	}
	if w.QueueMaxAgeHours != nil {
		cfg.QueueMaxAgeHours = *w.QueueMaxAgeHours
	}
	if w.RequestTimeoutSec != nil {
		cfg.RequestTimeoutSec = *w.RequestTimeoutSec
	}
	if w.PowerGain != nil {
		cfg.PowerGain = *w.PowerGain
	}
	if w.Buzzer != nil {
		cfg.Buzzer = *w.Buzzer
	}
	if w.LogLevel != "" {
		cfg.LogLevel = w.LogLevel
	}

	if err := validateAPIHost(w.APIHost); err != nil {
		fail("apiHost: %s", err)
	}
	if w.DataDir == "" || !filepath.IsAbs(w.DataDir) {
		fail("dataDir: 절대 경로여야 함")
	}
	if cfg.DebounceSec < 1 || cfg.DebounceSec > 3600 {
		fail("debounceSec: 1~3600 범위여야 함 (현재 %d)", cfg.DebounceSec)
	}
	if cfg.QueueMaxAgeHours < 1 || cfg.QueueMaxAgeHours > 24 {
		fail("queueMaxAgeHours: 1~24 범위여야 함 (현재 %d)", cfg.QueueMaxAgeHours)
	}
	if cfg.RequestTimeoutSec < 1 || cfg.RequestTimeoutSec > 30 {
		fail("requestTimeoutSec: 1~30 범위여야 함 (현재 %d)", cfg.RequestTimeoutSec)
	}
	if cfg.PowerGain < 50 || cfg.PowerGain > 300 {
		fail("powerGain: 50~300 범위여야 함 (현재 %d)", cfg.PowerGain)
	}
	if cfg.Buzzer != 0 && cfg.Buzzer != 1 {
		fail("buzzer: 0 또는 1 이어야 함 (현재 %d)", cfg.Buzzer)
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		fail("logLevel: debug|info|warn|error 중 하나여야 함")
	}

	if len(w.Readers) < 1 || len(w.Readers) > MaxReaders {
		fail("readers: 1~%d 개여야 함 (현재 %d)", MaxReaders, len(w.Readers))
	}

	seenID := map[string]bool{}
	seenAddr := map[string]bool{}
	seenToken := map[string]bool{}
	for i, r := range w.Readers {
		at := fmt.Sprintf("readers[%d]", i)
		if !readerIDPattern.MatchString(r.ID) {
			fail("%s.id: 영숫자로 시작하는 1~64자 [a-zA-Z0-9_-] 여야 함", at)
		}
		if seenID[r.ID] {
			fail("%s.id: 중복 %q", at, r.ID)
		}
		seenID[r.ID] = true

		if err := validateAddr(r.Addr); err != nil {
			fail("%s.addr: %s", at, err)
		}
		if seenAddr[r.Addr] {
			fail("%s.addr: 중복 %q", at, r.Addr)
		}
		seenAddr[r.Addr] = true

		tok := strings.ToLower(r.PulseToken)
		if !isHex64(tok) {
			// 토큰 원문은 절대 오류에 넣지 않는다.
			fail("%s.pulseToken: 정확히 64자리 hex 여야 함 (현재 %d자)", at, len(r.PulseToken))
		}
		if seenToken[tok] {
			fail("%s.pulseToken: 다른 리더와 중복", at)
		}
		seenToken[tok] = true

		cfg.Readers = append(cfg.Readers, Reader{ID: r.ID, Addr: r.Addr, Token: domain.NewSecret(tok)})
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("설정 검증 실패:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return cfg, nil
}

func validateAPIHost(host string) error {
	if host == "" {
		return fmt.Errorf("필수")
	}
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("URL 파싱 실패")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("userinfo/path/query/fragment 금지")
	}
	if u.Host == "" {
		return fmt.Errorf("host 없음")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		// 테스트에서만 loopback HTTP 허용 (설계서 §8.1)
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
		return fmt.Errorf("HTTP 는 loopback 에서만 허용")
	default:
		return fmt.Errorf("https 여야 함")
	}
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("host:port 형식이어야 함")
	}
	if host == "" {
		return fmt.Errorf("host 없음")
	}
	if port == "PORT" {
		return fmt.Errorf("미확정 placeholder PORT — 실제 데이터 포트를 확인해 기입해야 함")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("port 는 1~65535 숫자여야 함")
	}
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Token 은 CredentialProvider 구현이다 — 큐 행은 readerId 만 저장하고
// 전송 시 현재 설정에서 토큰을 찾는다 (계획서 §5.2).
func (c *Config) Token(readerID string) (domain.Secret, bool) {
	for _, r := range c.Readers {
		if r.ID == readerID {
			return r.Token, true
		}
	}
	return domain.Secret{}, false
}

func (c *Config) Reader(readerID string) (Reader, bool) {
	for _, r := range c.Readers {
		if r.ID == readerID {
			return r, true
		}
	}
	return Reader{}, false
}
