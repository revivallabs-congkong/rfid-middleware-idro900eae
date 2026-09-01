package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

var t0 = time.Date(2026, 9, 1, 10, 0, 0, 0, time.FixedZone("KST", 9*3600))

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func admitAt(t *testing.T, s *Store, reader, epc string, at time.Time) AdmitResult {
	t.Helper()
	res, err := s.Admit(reader, epc, at, at.Format(time.RFC3339Nano), 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDebounceBoundary(t *testing.T) {
	s, _ := openTemp(t)
	// 최초 수락
	if r := admitAt(t, s, "gate-a", "ABCD", t0); !r.Accepted {
		t.Fatal("최초 스캔은 수락돼야 함")
	}
	// 59.999초 — drop (설계서 §14.3 경계)
	if r := admitAt(t, s, "gate-a", "ABCD", t0.Add(59*time.Second+999*time.Millisecond)); r.Accepted {
		t.Error("59.999초는 drop")
	}
	// 정확히 60초 — accept
	if r := admitAt(t, s, "gate-a", "ABCD", t0.Add(60*time.Second)); !r.Accepted {
		t.Error("60초는 accept")
	}
}

func TestDebounceKeyIsReaderAndEPC(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "ABCD", t0)
	// 다른 reader 의 같은 EPC 는 각각 accept (설계서 §14.3)
	if r := admitAt(t, s, "gate-b", "ABCD", t0.Add(time.Second)); !r.Accepted {
		t.Error("다른 reader 는 독립 디바운스")
	}
	// 같은 reader 의 다른 EPC 도 accept
	if r := admitAt(t, s, "gate-a", "EEFF", t0.Add(time.Second)); !r.Accepted {
		t.Error("다른 EPC 는 독립 디바운스")
	}
}

// B2: 같은 UID 1초 간격 100회 → 수락 최대 2건 (0초·60초 부근)
func TestAcceptanceB2HundredScans(t *testing.T) {
	s, _ := openTemp(t)
	accepted := 0
	for i := 0; i < 100; i++ {
		if r := admitAt(t, s, "gate-a", "E28068940000501EC2205F7B", t0.Add(time.Duration(i)*time.Second)); r.Accepted {
			accepted++
		}
	}
	if accepted != 2 {
		t.Errorf("수락 %d건, 정확히 2건이어야 함 (0초, 60초)", accepted)
	}
	depth, _ := s.Depth()
	if depth != 2 {
		t.Errorf("큐 길이 %d, want 2", depth)
	}
}

func TestClockBackwardClamp(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "ABCD", t0)
	r := admitAt(t, s, "gate-a", "ABCD", t0.Add(-10*time.Minute))
	if r.Accepted {
		t.Error("시계 역행 시 elapsed 0 clamp → drop")
	}
	if !r.ClockBackward {
		t.Error("CLOCK_MOVED_BACKWARD 신호가 있어야 함")
	}
}

// B3 의 저장 부분: 재기동 후 큐·디바운스가 살아 있고 원래 checkedAt 이 보존된다
func TestRestartRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	checked := t0.Format(time.RFC3339Nano)
	if _, err := s.Admit("gate-a", "ABCD", t0, checked, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	it, err := s2.NextDue(t0.UnixMilli(), []string{"gate-a"})
	if err != nil {
		t.Fatal(err)
	}
	if it == nil || it.CheckedAt != checked || it.EPC != "ABCD" {
		t.Fatalf("재기동 후 원래 checkedAt 보존 실패: %+v", it)
	}
	// 재시작 직후에도 디바운스 유지 (ADR-003)
	r, err := s2.Admit("gate-a", "ABCD", t0.Add(30*time.Second), checked, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Accepted {
		t.Error("재시작 후에도 60초 내 재스캔은 drop")
	}
}

func TestNextDueOrderingAndRetrySkip(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "AAAA", t0)
	admitAt(t, s, "gate-a", "BBBB", t0.Add(time.Second))
	now := t0.Add(2 * time.Second).UnixMilli()

	it, _ := s.NextDue(now, []string{"gate-a"})
	if it == nil || it.EPC != "AAAA" {
		t.Fatalf("가장 이른 checked_at 우선: %+v", it)
	}
	// AAAA 실패 → 미래로 미룸 → BBBB 가 선택된다 (ADR-004: 실패가 다른 due 를 막지 않음)
	if err := s.RetryLater(it.ID, now+5000, "SERVER_5XX", 500); err != nil {
		t.Fatal(err)
	}
	it2, _ := s.NextDue(now, []string{"gate-a"})
	if it2 == nil || it2.EPC != "BBBB" {
		t.Fatalf("실패 행이 다른 due 를 막음: %+v", it2)
	}
	// backoff 시각 도래 후 AAAA 재선택, checkedAt 불변 (불변식 2)
	it3, _ := s.NextDue(now+5000, []string{"gate-a"})
	if it3 == nil || it3.EPC != "AAAA" || it3.AttemptCount != 1 {
		t.Fatalf("backoff 후 재선택 실패: %+v", it3)
	}
	if it3.CheckedAt != t0.Format(time.RFC3339Nano) {
		t.Error("재시도에서 checkedAt 이 바뀜")
	}
}

func TestNextDueRespectsGateFilter(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "AAAA", t0)
	admitAt(t, s, "gate-b", "BBBB", t0)
	now := t0.Add(time.Second).UnixMilli()

	it, _ := s.NextDue(now, []string{"gate-b"})
	if it == nil || it.ReaderID != "gate-b" {
		t.Fatalf("suspended reader 행이 선택됨: %+v", it)
	}
	it2, _ := s.NextDue(now, nil)
	if it2 != nil {
		t.Fatal("sendable reader 가 없으면 nil")
	}
}

func TestCompleteDeletes(t *testing.T) {
	s, _ := openTemp(t)
	r := admitAt(t, s, "gate-a", "AAAA", t0)
	if err := s.Complete(r.ItemID); err != nil {
		t.Fatal(err)
	}
	depth, _ := s.Depth()
	if depth != 0 {
		t.Errorf("depth = %d", depth)
	}
}

func TestExpire24h(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "OLD1", t0)
	admitAt(t, s, "gate-a", "NEW1", t0.Add(2*time.Hour))
	cutoff := t0.Add(24 * time.Hour).Add(time.Minute).UnixMilli() // t0 + 24h 1m
	expired, err := s.ExpireBefore(cutoff - 24*3600*1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].EPC != "OLD1" {
		t.Fatalf("만료 대상: %+v", expired)
	}
	depth, _ := s.Depth()
	if depth != 1 {
		t.Errorf("남은 행 %d, want 1", depth)
	}
}

func TestCleanupDebounceKeepsPending(t *testing.T) {
	s, _ := openTemp(t)
	r := admitAt(t, s, "gate-a", "AAAA", t0)
	// pending 큐가 있으면 디바운스 행을 지우지 않는다
	n, err := s.CleanupDebounce(t0.Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("pending 있는 디바운스 행이 삭제됨")
	}
	s.Complete(r.ItemID)
	n, _ = s.CleanupDebounce(t0.Add(time.Hour).UnixMilli())
	if n != 1 {
		t.Errorf("정리 %d행, want 1", n)
	}
}

func TestGatePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	s, _ := Open(path)
	meta := &domain.GateMeta{EventName: "행사", BoothName: "A 게이트", UnitName: "본관", CooldownSec: 60}
	if err := s.SetGate("gate-a", domain.GateActive, "", t0.UnixMilli(), "fp123", meta); err != nil {
		t.Fatal(err)
	}
	// meta nil 은 기존 meta 유지
	if err := s.SetGate("gate-a", domain.GateSuspendedToken, "404", t0.Add(time.Hour).UnixMilli(), "fp123", nil); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, _ := Open(path)
	defer s2.Close()
	gates, err := s2.Gates()
	if err != nil {
		t.Fatal(err)
	}
	g := gates["gate-a"]
	if g.State != domain.GateSuspendedToken || g.Meta.BoothName != "A 게이트" || g.TokenFingerprint != "fp123" {
		t.Errorf("gate 복원 실패: %+v", g)
	}
}

func TestDiscardPending(t *testing.T) {
	s, _ := openTemp(t)
	admitAt(t, s, "gate-a", "AAAA", t0)
	admitAt(t, s, "gate-a", "BBBB", t0)
	admitAt(t, s, "gate-b", "CCCC", t0)
	n, err := s.DiscardPending("gate-a")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("discard %d, want 2", n)
	}
	if c, _ := s.PendingCount("gate-b"); c != 1 {
		t.Error("다른 reader 행이 삭제됨")
	}
}

func TestSchemaVersionTooHigh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	s, _ := Open(path)
	s.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(99, 'x')`)
	s.Close()
	if _, err := Open(path); err == nil {
		t.Error("미지원 schema version 은 거부돼야 함")
	}
}
