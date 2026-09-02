package session

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// fakeReader 는 명령 byte 를 캡처하고 응답을 조절하는 가짜 IDRO900EAE 다 (설계서 §14.2).
type fakeReader struct {
	conn net.Conn
	mu   sync.Mutex
	cmds [][]byte
	// respond 가 nil 이면 기본 정상 응답.
	respond func(cmd []byte) []byte
}

func defaultRespond(cmd []byte) []byte {
	switch string(cmd) {
	case ">y v\r":
		return []byte(">v EAE25061900\r\n")
	case ">x b 0\r":
		return []byte(">b 0\r\n")
	case ">x b 1\r":
		return []byte(">b 1\r\n")
	case ">x p 300\r":
		return []byte(">p 300\r\n")
	case ">x p 200\r":
		return []byte(">p 200\r\n")
	case ">x i 0\r":
		return []byte(">i 0\r\n")
	case ">f\r":
		return []byte(">Af\r\n")
	case "3\r":
		return []byte(">A3\r\n")
	}
	return nil
}

// run 은 CR 단위로 명령을 읽어 응답한다.
func (f *fakeReader) run() {
	buf := make([]byte, 256)
	var acc []byte
	for {
		n, err := f.conn.Read(buf)
		if n > 0 {
			acc = append(acc, buf[:n]...)
			for {
				i := bytes.IndexByte(acc, 0x0D)
				if i < 0 {
					break
				}
				cmd := append([]byte{}, acc[:i+1]...)
				acc = acc[i+1:]
				f.mu.Lock()
				f.cmds = append(f.cmds, cmd)
				respond := f.respond
				f.mu.Unlock()
				if respond == nil {
					respond = defaultRespond
				}
				if resp := respond(cmd); resp != nil {
					f.conn.Write(resp)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (f *fakeReader) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.cmds))
	for i, c := range f.cmds {
		out[i] = string(c)
	}
	return out
}

func newTestSession(t *testing.T, handler Handler) (*Session, *fakeReader) {
	t.Helper()
	client, server := net.Pipe()
	fr := &fakeReader{conn: server}
	go fr.run()
	t.Cleanup(func() { client.Close(); server.Close() })

	log, _ := logging.New("", logging.Error, nil)
	s := New(Config{ReaderID: "gate-a", Addr: "test:1", PowerGain: 300, Buzzer: 0}, handler, log, clock.Real{})
	s.Dial = func(ctx context.Context) (net.Conn, error) { return client, nil }
	return s, fr
}

func TestInitSequenceAndTagFlow(t *testing.T) {
	var mu sync.Mutex
	var reads []domain.TagRead
	s, fr := newTestSession(t, func(r domain.TagRead) {
		mu.Lock()
		reads = append(reads, r)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	// Stop 선행 + 초기화 5개 = 6개 명령이 순서대로 도착할 때까지 대기
	deadline := time.After(3 * time.Second)
	for {
		if len(fr.commands()) >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("초기화 미완료: %q", fr.commands())
		case <-time.After(10 * time.Millisecond):
		}
	}
	want := []string{"3\r", ">y v\r", ">x b 0\r", ">x p 300\r", ">x i 0\r", ">f\r"}
	got := fr.commands()[:6]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cmd[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Inventory 스트림 — 태그 2건 (하나는 chunk 분할)
	fr.conn.Write([]byte(">T3000E28068940000501EC2205F7B\r\n"))
	fr.conn.Write([]byte(">T3000AA"))
	fr.conn.Write([]byte("BB\r\n"))

	deadline = time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(reads)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("태그 수신 실패")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if reads[0].EPC != "E28068940000501EC2205F7B" || reads[1].EPC != "AABB" {
		t.Errorf("EPC = %q, %q", reads[0].EPC, reads[1].EPC)
	}
	if reads[0].ReaderID != "gate-a" || reads[0].ReceivedAt.IsZero() {
		t.Errorf("TagRead 메타 오류: %+v", reads[0])
	}
	cancel()
	<-done
}

// INIT 완료 전 TagReply 는 admission 에 넣지 않는다 (불변식 9)
func TestTagsDuringInitNotAdmitted(t *testing.T) {
	var mu sync.Mutex
	var reads []domain.TagRead
	client, server := net.Pipe()
	fr := &fakeReader{conn: server}
	fr.respond = func(cmd []byte) []byte {
		// 버전 응답 앞에 오래된 Inventory 의 태그가 끼어든다
		if string(cmd) == ">y v\r" {
			return []byte(">T3000DEAD\r\n>v EAE25061900\r\n")
		}
		return defaultRespond(cmd)
	}
	go fr.run()
	t.Cleanup(func() { client.Close(); server.Close() })

	log, _ := logging.New("", logging.Error, nil)
	s := New(Config{ReaderID: "gate-a", Addr: "test:1", PowerGain: 300, Buzzer: 0}, func(r domain.TagRead) {
		mu.Lock()
		reads = append(reads, r)
		mu.Unlock()
	}, log, clock.Real{})
	s.Dial = func(ctx context.Context) (net.Conn, error) { return client, nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.After(3 * time.Second)
	for {
		if len(fr.commands()) >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("초기화 미완료: %q", fr.commands())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// init 후 정상 태그
	fr.conn.Write([]byte(">T3000BEEF\r\n"))
	deadline = time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(reads)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("init 후 태그 수신 실패")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range reads {
		if r.EPC == "DEAD" {
			t.Error("init 중 태그가 admission 으로 전달됨")
		}
	}
}

// RDR2: Inventory 중 Stop → 설정 → >f 재개가 파서를 깨뜨리지 않음
func TestReconfigureStopApplyResume(t *testing.T) {
	var mu sync.Mutex
	var reads []domain.TagRead
	s, fr := newTestSession(t, func(r domain.TagRead) {
		mu.Lock()
		reads = append(reads, r)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.After(3 * time.Second)
	for {
		if len(fr.commands()) >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("초기화 미완료")
		case <-time.After(10 * time.Millisecond):
		}
	}

	s.RequestPower(200)
	// Stop(3\r) → >x p 200 → >f 순서 확인
	deadline = time.After(3 * time.Second)
	for {
		cmds := fr.commands()
		if len(cmds) >= 9 {
			if cmds[6] != "3\r" || cmds[7] != ">x p 200\r" || cmds[8] != ">f\r" {
				t.Fatalf("reconfigure 순서 오류: %q", cmds[6:])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("reconfigure 미완료: %q", fr.commands())
		case <-time.After(10 * time.Millisecond):
		}
	}

	// 재개 후 후속 TagReply 정상 (RDR2)
	fr.conn.Write([]byte(">T3000CAFE\r\n"))
	deadline = time.After(3 * time.Second)
	for {
		mu.Lock()
		ok := len(reads) > 0 && reads[len(reads)-1].EPC == "CAFE"
		mu.Unlock()
		if ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("재개 후 태그 수신 실패")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// 응답 timeout → 재전송 없이 세션 실패 → 재접속 (설계서 §6.4)
func TestInitTimeoutTriggersReconnect(t *testing.T) {
	dialCount := 0
	var mu sync.Mutex
	log, _ := logging.New("", logging.Error, nil)
	s := New(Config{ReaderID: "gate-a", Addr: "test:1", PowerGain: 300, Buzzer: 0},
		func(domain.TagRead) {}, log, clock.Real{})
	s.Dial = func(ctx context.Context) (net.Conn, error) {
		mu.Lock()
		dialCount++
		mu.Unlock()
		client, server := net.Pipe()
		// 무응답 리더 — 읽기만 하고 아무것도 답하지 않는다
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := server.Read(buf); err != nil {
					return
				}
			}
		}()
		return client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	s.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	// 2초 timeout + 1초 backoff → 4초 안에 최소 2회 접속 시도
	if dialCount < 2 {
		t.Errorf("재접속 시도 %d회 — timeout 후 재접속해야 함", dialCount)
	}
}

func TestReconnectDelaySchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{{0, time.Second}, {1, 5 * time.Second}, {2, 30 * time.Second}, {9, 30 * time.Second}}
	for _, c := range cases {
		if got := reconnectDelay(c.attempt); got != c.want {
			t.Errorf("reconnectDelay(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}
