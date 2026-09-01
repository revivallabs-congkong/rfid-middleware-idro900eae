package protocol

import (
	"bufio"
	"os"
	"testing"
)

// 2026-09-01 현장 시험에서 관측한 실장비(IDRO900EAE, 펌웨어 EAE26081902) raw
// 라인 회귀 시험. 벤더 문서 예시와 달리 실장비 >T 의 PC 는 3400 이었다.
func TestParseRealDeviceLines(t *testing.T) {
	f, err := os.Open("../../../testdata/reader-lines/idro900eae-real-20260901.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	want := []struct {
		kind    Kind
		setting Setting
		ack     byte
		pc, epc string
	}{
		{kind: KindSetting, setting: Setting{Type: 'v', Value: "EAE26081902"}},
		{kind: KindSetting, setting: Setting{Type: 'b', Value: "1"}},
		{kind: KindSetting, setting: Setting{Type: 'p', Value: "300"}},
		{kind: KindSetting, setting: Setting{Type: 'i', Value: "0"}},
		{kind: KindAck, ack: 'f'},
		{kind: KindTag, pc: "3400", epc: "E2801170000002155EDD7076"},
	}

	sc := bufio.NewScanner(f)
	i := 0
	for sc.Scan() {
		if i >= len(want) {
			t.Fatalf("fixture 라인이 기대보다 많음: %q", sc.Text())
		}
		w := want[i]
		l := Parse([]byte(sc.Text()))
		if l.Kind != w.kind {
			t.Errorf("line %d %q: kind = %v, want %v (err=%q)", i+1, sc.Text(), l.Kind, w.kind, l.Err)
		}
		switch w.kind {
		case KindSetting:
			if l.Setting != w.setting {
				t.Errorf("line %d: setting = %+v, want %+v", i+1, l.Setting, w.setting)
			}
		case KindAck:
			if l.Ack != w.ack {
				t.Errorf("line %d: ack = %q, want %q", i+1, l.Ack, w.ack)
			}
		case KindTag:
			if l.Tag.PC != w.pc || l.Tag.EPC != w.epc || l.Tag.HasRSSI {
				t.Errorf("line %d: tag = %+v, want PC=%s EPC=%s RSSI 없음", i+1, l.Tag, w.pc, w.epc)
			}
		}
		i++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(want) {
		t.Fatalf("fixture 라인 %d개, 기대 %d개", i, len(want))
	}
}
