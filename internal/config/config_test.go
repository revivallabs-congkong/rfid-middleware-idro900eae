package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const validToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validJSON(mutate func(m map[string]any)) string {
	m := map[string]any{
		"version": 1,
		"apiHost": "https://api.congkong.net",
		"dataDir": "/var/lib/rfid",
		"readers": []map[string]any{
			{"id": "gate-a", "addr": "192.168.9.6:5000", "pulseToken": validToken},
		},
	}
	if mutate != nil {
		mutate(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestParseValid(t *testing.T) {
	cfg, err := Parse(strings.NewReader(validJSON(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DebounceSec != 60 || cfg.QueueMaxAgeHours != 24 || cfg.RequestTimeoutSec != 10 ||
		cfg.PowerGain != 300 || cfg.Buzzer != 0 || cfg.LogLevel != "info" {
		t.Errorf("기본값 오류: %+v", cfg)
	}
	if len(cfg.Readers) != 1 || cfg.Readers[0].ID != "gate-a" {
		t.Errorf("readers: %+v", cfg.Readers)
	}
	if cfg.Readers[0].Token.Raw() != validToken {
		t.Error("토큰이 canonical lowercase 로 보존돼야 함")
	}
}

func TestTokenCanonicalLowercase(t *testing.T) {
	upper := strings.ToUpper(validToken)
	cfg, err := Parse(strings.NewReader(validJSON(func(m map[string]any) {
		m["readers"].([]map[string]any)[0]["pulseToken"] = upper
	})))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Readers[0].Token.Raw() != validToken {
		t.Error("대문자 토큰은 소문자로 정규화돼야 함")
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"unknown-field", func(m map[string]any) { m["debounceSecs"] = 60 }},
		{"no-version", func(m map[string]any) { delete(m, "version") }},
		{"bad-version", func(m map[string]any) { m["version"] = 2 }},
		{"http-non-loopback", func(m map[string]any) { m["apiHost"] = "http://api.congkong.net" }},
		{"host-with-path", func(m map[string]any) { m["apiHost"] = "https://api.congkong.net/v3" }},
		{"host-with-userinfo", func(m map[string]any) { m["apiHost"] = "https://user@api.congkong.net" }},
		{"relative-datadir", func(m map[string]any) { m["dataDir"] = "data" }},
		{"debounce-zero", func(m map[string]any) { m["debounceSec"] = 0 }},
		{"debounce-over", func(m map[string]any) { m["debounceSec"] = 3601 }},
		{"queue-age-over", func(m map[string]any) { m["queueMaxAgeHours"] = 25 }},
		{"timeout-over", func(m map[string]any) { m["requestTimeoutSec"] = 31 }},
		{"power-under", func(m map[string]any) { m["powerGain"] = 49 }},
		{"buzzer-2", func(m map[string]any) { m["buzzer"] = 2 }},
		{"bad-loglevel", func(m map[string]any) { m["logLevel"] = "trace" }},
		{"no-readers", func(m map[string]any) { m["readers"] = []map[string]any{} }},
		{"port-placeholder", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["addr"] = "192.168.9.6:PORT"
		}},
		{"port-zero", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["addr"] = "192.168.9.6:0"
		}},
		{"no-port", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["addr"] = "192.168.9.6"
		}},
		{"short-token", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["pulseToken"] = "abc123"
		}},
		{"non-hex-token", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["pulseToken"] = strings.Repeat("z", 64)
		}},
		{"bad-reader-id", func(m map[string]any) {
			m["readers"].([]map[string]any)[0]["id"] = "-starts-with-dash"
		}},
		{"dup-readers", func(m map[string]any) {
			m["readers"] = []map[string]any{
				{"id": "gate-a", "addr": "192.168.9.6:5000", "pulseToken": validToken},
				{"id": "gate-a", "addr": "192.168.9.7:5000", "pulseToken": strings.Repeat("a", 64)},
			}
		}},
		{"dup-token", func(m map[string]any) {
			m["readers"] = []map[string]any{
				{"id": "gate-a", "addr": "192.168.9.6:5000", "pulseToken": validToken},
				{"id": "gate-b", "addr": "192.168.9.7:5000", "pulseToken": strings.ToUpper(validToken)},
			}
		}},
		{"nine-readers", func(m map[string]any) {
			var rs []map[string]any
			for i := 0; i < 9; i++ {
				tok := fmt.Sprintf("%064x", i)
				rs = append(rs, map[string]any{
					"id": fmt.Sprintf("gate-%d", i), "addr": fmt.Sprintf("192.168.9.%d:5000", i+1), "pulseToken": tok,
				})
			}
			m["readers"] = rs
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(validJSON(c.mutate)))
			if err == nil {
				t.Fatal("거부돼야 함")
			}
			// 오류 문자열에 토큰 원문 금지 (계획서 단계 0 redaction)
			if strings.Contains(err.Error(), validToken) || strings.Contains(err.Error(), strings.ToUpper(validToken)) {
				t.Errorf("오류에 토큰 원문 포함: %s", err)
			}
		})
	}
}

func TestLoopbackHTTPAllowed(t *testing.T) {
	_, err := Parse(strings.NewReader(validJSON(func(m map[string]any) {
		m["apiHost"] = "http://127.0.0.1:8080"
	})))
	if err != nil {
		t.Errorf("loopback HTTP 는 테스트용으로 허용: %v", err)
	}
}

func TestJSONCRejected(t *testing.T) {
	in := "// comment\n" + validJSON(nil)
	if _, err := Parse(strings.NewReader(in)); err == nil {
		t.Error("주석 있는 JSONC 는 거부돼야 함")
	}
}

func TestSecretNeverPrints(t *testing.T) {
	cfg, err := Parse(strings.NewReader(validJSON(nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Readers[0].Token
	for _, out := range []string{
		fmt.Sprintf("%v", s), fmt.Sprintf("%s", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s),
	} {
		if strings.Contains(out, validToken) {
			t.Errorf("Secret 이 원문을 출력함: %s", out)
		}
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), validToken) {
		t.Error("Secret JSON marshal 이 원문을 출력함")
	}
	b, _ = json.Marshal(cfg.Readers[0])
	if strings.Contains(string(b), validToken) {
		t.Error("Reader JSON marshal 이 토큰을 출력함")
	}
}

func TestTokenLookup(t *testing.T) {
	cfg, _ := Parse(strings.NewReader(validJSON(nil)))
	if _, ok := cfg.Token("gate-a"); !ok {
		t.Error("gate-a 토큰이 있어야 함")
	}
	if _, ok := cfg.Token("nope"); ok {
		t.Error("없는 reader 는 실패해야 함")
	}
}
