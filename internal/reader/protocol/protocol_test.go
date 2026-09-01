package protocol

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- 명령 byte golden (계획서 단계 1: byte-level test) ---

func TestCommandBytes(t *testing.T) {
	buzzer0, err := CmdSetBuzzer(0)
	if err != nil {
		t.Fatal(err)
	}
	power300, err := CmdSetPower(300)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"version", CmdVersion(), []byte{0x3E, 0x79, 0x20, 0x76, 0x0D}},
		{"buzzer0", buzzer0, []byte(">x b 0\r")},
		{"power300", power300, []byte(">x p 300\r")},
		{"rssi-off", CmdRSSIOff(), []byte(">x i 0\r")},
		{"inventory", CmdStartInventory(), []byte{0x3E, 0x66, 0x0D}},
		{"stop", CmdStop(), []byte{0x33, 0x0D}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % X want % X", c.name, c.got, c.want)
		}
		// 모든 명령은 CR 단독으로 끝난다. LF 가 붙으면 리더가 무시한다.
		if c.got[len(c.got)-1] != 0x0D {
			t.Errorf("%s: EOL 이 CR 이 아님", c.name)
		}
		if bytes.Contains(c.got, []byte{0x0A}) {
			t.Errorf("%s: LF 포함 금지", c.name)
		}
	}
	// Stop 만 헤더 > 가 없다 (불변식 8).
	if CmdStop()[0] == '>' {
		t.Error("stop 에 헤더 > 가 있으면 안 됨")
	}
	if _, err := CmdSetBuzzer(2); err == nil {
		t.Error("buzzer 2 는 거부돼야 함")
	}
	if _, err := CmdSetPower(49); err == nil {
		t.Error("power 49 는 거부돼야 함")
	}
	if _, err := CmdSetPower(301); err == nil {
		t.Error("power 301 은 거부돼야 함")
	}
}

// 금지 명령이 소스에 존재하지 않는지 정적 검사 (계획서 단계 0).
func TestNoForbiddenCommandLiterals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{`">k`, `">l`, `">u`, `">w`} // Kill, Lock, Untraceable, Memory Write
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range forbidden {
			if strings.Contains(string(b), lit) {
				t.Errorf("%s: 금지 명령 literal %s 발견", f, lit)
			}
		}
	}
}

// --- >T 파싱 픽스처 ---

func TestParseTag(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		kind    Kind
		epc     string
		pc      string
		rssi    float64
		hasRSSI bool
	}{
		{"compact", ">T3000E28068940000501EC2205F7B", KindTag, "E28068940000501EC2205F7B", "3000", 0, false},
		{"spaced", ">T 3000 E28068940000501EC2205F7B", KindTag, "E28068940000501EC2205F7B", "3000", 0, false},
		{"rssi", ">T 3000 E28068940000501EC2205F7B ;RFD96", KindTag, "E28068940000501EC2205F7B", "3000", -61.8, true},
		{"rssi-compact", ">T3000E28068940000501EC2205F7B;RFD96", KindTag, "E28068940000501EC2205F7B", "3000", -61.8, true},
		{"lowercase-input", ">T3000e28068940000501ec2205f7b", KindTag, "E28068940000501EC2205F7B", "3000", 0, false},
		{"short-epc", ">T3000AB", KindTag, "AB", "3000", 0, false},
		{"long-epc", ">T30001111222233334444555566667777888899990000", KindTag, "1111222233334444555566667777888899990000", "3000", 0, false},
		{"positive-rssi", ">T3000ABCD;R0010", KindTag, "ABCD", "3000", 1.6, true},
		// malformed — 세션을 죽이지 않고 BadTag 로 카운트만 (설계서 §6.2)
		{"short-pc", ">T300", KindBadTag, "", "", 0, false},
		{"empty-epc", ">T3000", KindBadTag, "", "", 0, false},
		{"odd-epc", ">T3000ABC", KindBadTag, "", "", 0, false},
		{"non-hex-epc", ">T3000ZZZZ", KindBadTag, "", "", 0, false},
		{"separator-epc", ">T300004:A2:2B:5C", KindBadTag, "", "", 0, false},
		{"bad-rssi-len", ">T3000ABCD;RFD9", KindBadTag, "", "", 0, false},
		{"bad-rssi-hex", ">T3000ABCD;RZZZZ", KindBadTag, "", "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := Parse([]byte(c.in))
			if l.Kind != c.kind {
				t.Fatalf("kind = %v, want %v (err=%s)", l.Kind, c.kind, l.Err)
			}
			if c.kind != KindTag {
				return
			}
			if l.Tag.EPC != c.epc {
				t.Errorf("EPC = %q, want %q", l.Tag.EPC, c.epc)
			}
			if l.Tag.PC != c.pc {
				t.Errorf("PC = %q, want %q", l.Tag.PC, c.pc)
			}
			if l.Tag.HasRSSI != c.hasRSSI {
				t.Errorf("HasRSSI = %v, want %v", l.Tag.HasRSSI, c.hasRSSI)
			}
			if c.hasRSSI && l.Tag.RSSIdBm() != c.rssi {
				t.Errorf("RSSI = %v, want %v", l.Tag.RSSIdBm(), c.rssi)
			}
			// EPC 는 항상 대문자·무구분자
			if l.Tag.EPC != strings.ToUpper(l.Tag.EPC) || strings.ContainsAny(l.Tag.EPC, ":-. ") {
				t.Errorf("EPC 정규화 위반: %q", l.Tag.EPC)
			}
		})
	}
}

func TestParseOtherKinds(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
	}{
		{">Af", KindAck},
		{">A3", KindAck},
		{">Ar", KindAck},
		{">C3000E2806894000050101122334455665566 01", KindCode},
		{">p 300", KindSetting},
		{">v EAE25061900", KindSetting},
		{">b 0", KindSetting},
		{">i 0", KindSetting},
		{"", KindUnknown},
		{">", KindUnknown},
		{"garbage", KindUnknown},
		{">zunknown", KindUnknown},
	}
	for _, c := range cases {
		l := Parse([]byte(c.in))
		if l.Kind != c.kind {
			t.Errorf("Parse(%q).Kind = %v, want %v", c.in, l.Kind, c.kind)
		}
	}
	if l := Parse([]byte(">Af")); l.Ack != 'f' {
		t.Errorf("Ack = %c, want f", l.Ack)
	}
	if l := Parse([]byte(">A3")); l.Ack != '3' {
		t.Errorf("Ack = %c, want 3", l.Ack)
	}
	if l := Parse([]byte(">p 300")); l.Setting.Type != 'p' || l.Setting.Value != "300" {
		t.Errorf("Setting = %+v", l.Setting)
	}
}

// --- 프레이머 ---

func TestFramerBasic(t *testing.T) {
	f := NewFramer(0)
	res := f.Push([]byte(">Af\r\n>T3000ABCD\r\n"))
	if len(res.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(res.Lines))
	}
	if string(res.Lines[0]) != ">Af" || string(res.Lines[1]) != ">T3000ABCD" {
		t.Errorf("lines = %q", res.Lines)
	}
	if f.PendingTail() != 0 {
		t.Errorf("tail = %d", f.PendingTail())
	}
}

// 모든 byte 위치에서 chunk 를 잘라 재조립 (계획서 단계 1 table test)
func TestFramerAllSplitPositions(t *testing.T) {
	stream := []byte(">T 3000 E28068940000501EC2205F7B ;RFD96\r\n>Af\r\n\r\n>T3000AB\r\n")
	want := []string{">T 3000 E28068940000501EC2205F7B ;RFD96", ">Af", "", ">T3000AB"}
	for cut := 0; cut <= len(stream); cut++ {
		f := NewFramer(0)
		var got []string
		for _, line := range f.Push(stream[:cut]).Lines {
			got = append(got, string(line))
		}
		for _, line := range f.Push(stream[cut:]).Lines {
			got = append(got, string(line))
		}
		if len(got) != len(want) {
			t.Fatalf("cut=%d: %d lines, want %d (%q)", cut, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("cut=%d: line[%d] = %q, want %q", cut, i, got[i], want[i])
			}
		}
	}
}

// 라인 중간 다중 분할 (byte 단위)
func TestFramerByteByByte(t *testing.T) {
	stream := []byte(">T3000ABCD\r\n>A3\r\n")
	f := NewFramer(0)
	var got []string
	for i := 0; i < len(stream); i++ {
		for _, line := range f.Push(stream[i : i+1]).Lines {
			got = append(got, string(line))
		}
	}
	if len(got) != 2 || got[0] != ">T3000ABCD" || got[1] != ">A3" {
		t.Fatalf("got %q", got)
	}
}

func TestFramerOversizeRecovery(t *testing.T) {
	f := NewFramer(16)
	big := bytes.Repeat([]byte("A"), 100)
	res := f.Push(big)
	if len(res.Lines) != 0 {
		t.Fatal("과대 frame 중 라인이 나오면 안 됨")
	}
	// delimiter 도달 → 과대 frame 폐기 1건, 이후 정상 복구
	res = f.Push([]byte("BBB\r\n>Af\r\n"))
	if res.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", res.Dropped)
	}
	if len(res.Lines) != 1 || string(res.Lines[0]) != ">Af" {
		t.Errorf("lines = %q", res.Lines)
	}
}

// 과대 frame 이 CR 에서 끊긴 경우에도 CRLF 분할 감지가 유지되는지
func TestFramerOversizeSplitAtCR(t *testing.T) {
	f := NewFramer(8)
	f.Push(append(bytes.Repeat([]byte("A"), 50), 0x0D))
	res := f.Push([]byte{0x0A})
	if res.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", res.Dropped)
	}
	res = f.Push([]byte(">A3\r\n"))
	if len(res.Lines) != 1 || string(res.Lines[0]) != ">A3" {
		t.Errorf("복구 실패: %q", res.Lines)
	}
}

func TestFramerCROnlyIsNotDelimiter(t *testing.T) {
	f := NewFramer(0)
	res := f.Push([]byte(">Af\r"))
	if len(res.Lines) != 0 {
		t.Fatal("CR 단독은 프레임 경계가 아님")
	}
	res = f.Push([]byte("\n"))
	if len(res.Lines) != 1 || string(res.Lines[0]) != ">Af" {
		t.Fatalf("got %q", res.Lines)
	}
}

// --- 퍼즈: 어떤 chunk 경계·malformed 입력에도 panic 없음 ---

func FuzzFramerParse(f *testing.F) {
	f.Add([]byte(">T3000ABCD\r\n"), uint8(3))
	f.Add([]byte(">T 3000 E28068940000501EC2205F7B ;RFD96\r\n>Af\r\n"), uint8(1))
	f.Add([]byte("\r\n\r\n>C00 01\r\n"), uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, step uint8) {
		fr := NewFramer(64)
		n := int(step)%7 + 1
		for i := 0; i < len(data); i += n {
			end := i + n
			if end > len(data) {
				end = len(data)
			}
			for _, line := range fr.Push(data[i:end]).Lines {
				Parse(line)
			}
		}
	})
}
