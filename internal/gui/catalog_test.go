package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCat(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pulse-sessions.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const tok1 = "8623000000000000000000000000000000000000000000000000000000000001"
const tok2 = "9950000000000000000000000000000000000000000000000000000000000002"

func TestLoadCatalogOK(t *testing.T) {
	p := writeCat(t, `{"version":1,"eventName":"E","exportedAt":"2026-09-02T10:00:00+09:00",
	  "sessions":[
	    {"id":"s1","name":"세션1","unitName":"Session 1","tokenLabel":"Gate A","pulseToken":"`+tok1+`","issuedAt":"2026-09-01T15:00:00+09:00"},
	    {"id":"s2","name":"세션2","unitName":"Session 2","tokenLabel":"","pulseToken":"`+tok2+`","extraField":"ignored"}
	  ],"futureTop":123}`)
	c, err := LoadCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sessions) != 2 || len(c.Warnings) != 0 {
		t.Fatalf("sessions=%d warnings=%v", len(c.Sessions), c.Warnings)
	}
	if c.Sessions[0].TokenFP != tok1[:8] {
		t.Fatalf("fp=%s", c.Sessions[0].TokenFP)
	}
}

func TestLoadCatalogVersionReject(t *testing.T) {
	p := writeCat(t, `{"version":2,"eventName":"E","exportedAt":"x","sessions":[{"id":"a","name":"n","unitName":"u","pulseToken":"`+tok1+`"}]}`)
	if _, err := LoadCatalog(p); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version 거부 실패: %v", err)
	}
}

func TestLoadCatalogItemErrors(t *testing.T) {
	p := writeCat(t, `{"version":1,"eventName":"E","exportedAt":"x","sessions":[
	  {"id":"s1","name":"a","unitName":"u","pulseToken":"`+tok1+`"},
	  {"id":"s1","name":"dup","unitName":"u","pulseToken":"`+tok2+`"},
	  {"id":"s3","name":"bad","unitName":"u","pulseToken":"ABCDEF"},
	  {"id":"","name":"x","unitName":"u","pulseToken":"`+tok2+`"}]}`)
	c, err := LoadCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sessions) != 1 || len(c.Warnings) != 3 {
		t.Fatalf("sessions=%d warnings=%v", len(c.Sessions), c.Warnings)
	}
}
