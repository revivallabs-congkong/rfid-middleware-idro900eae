// Package sqlite 는 영속 큐·디바운스·gate 상태의 원자적 저장소다 (설계서 §7).
// 순수 Go 드라이버(modernc.org/sqlite)를 사용해 CGO 없이 실행한다 (ADR-002).
package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

// Store 는 단일 연결로 모든 접근을 직렬화한다 — AdmissionService 직렬화와
// 단일 sender 전제에서 충분하며 트랜잭션 경합을 없앤다 (설계서 §7.3).
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// Open 은 DB 를 열고 PRAGMA 와 migration 을 적용한다.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=FULL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations 는 증가하는 정수 버전이다. 더 높은 미지원 schema version 이면
// 읽기/쓰기를 시작하지 않는다 (설계서 §7.1).
var migrations = []string{
	// version 1
	`CREATE TABLE queue_items (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  reader_id TEXT NOT NULL,
	  epc TEXT NOT NULL,
	  checked_at TEXT NOT NULL,
	  checked_at_unix_ms INTEGER NOT NULL,
	  enqueued_at_unix_ms INTEGER NOT NULL,
	  attempt_count INTEGER NOT NULL DEFAULT 0,
	  next_attempt_at_unix_ms INTEGER NOT NULL,
	  last_error_class TEXT NOT NULL DEFAULT '',
	  last_http_status INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX idx_queue_due
	  ON queue_items(next_attempt_at_unix_ms, checked_at_unix_ms, id);
	CREATE INDEX idx_queue_reader
	  ON queue_items(reader_id, checked_at_unix_ms);
	CREATE TABLE debounce_state (
	  reader_id TEXT NOT NULL,
	  epc TEXT NOT NULL,
	  last_accepted_at_unix_ms INTEGER NOT NULL,
	  PRIMARY KEY(reader_id, epc)
	);
	CREATE TABLE gate_state (
	  reader_id TEXT PRIMARY KEY,
	  state TEXT NOT NULL,
	  reason TEXT NOT NULL DEFAULT '',
	  changed_at_unix_ms INTEGER NOT NULL,
	  token_fingerprint TEXT NOT NULL DEFAULT '',
	  event_name TEXT NOT NULL DEFAULT '',
	  booth_name TEXT NOT NULL DEFAULT '',
	  unit_name TEXT NOT NULL DEFAULT '',
	  cooldown_sec INTEGER NOT NULL DEFAULT 0
	);`,
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
	  version INTEGER PRIMARY KEY,
	  applied_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("migration table: %w", err)
	}
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("migration version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("DB schema version %d 은 이 바이너리(최대 %d)보다 높음 — 실행 거부", current, len(migrations))
	}
	for v := current; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			v+1, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration record %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// AdmitResult 는 원자적 admission 결과다.
type AdmitResult struct {
	Accepted      bool
	ItemID        int64
	ClockBackward bool // 시스템 시계가 마지막 수락 이전으로 이동 — CLOCK_MOVED_BACKWARD 로그용
}

// Admit 은 디바운스 검사·큐 삽입·디바운스 갱신을 한 트랜잭션으로 처리한다 (ADR-003).
// "전송" 은 서버 호출이 아니라 송신 파이프라인에 영속 수락된 시점이다.
func (s *Store) Admit(readerID, epc string, receivedAt time.Time, checkedAt string, debounce time.Duration) (AdmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receivedMS := receivedAt.UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return AdmitResult{}, err
	}
	defer tx.Rollback()

	var last int64
	var haveLast bool
	err = tx.QueryRow(`SELECT last_accepted_at_unix_ms FROM debounce_state WHERE reader_id=? AND epc=?`,
		readerID, epc).Scan(&last)
	switch err {
	case nil:
		haveLast = true
	case sql.ErrNoRows:
	default:
		return AdmitResult{}, err
	}

	res := AdmitResult{}
	if haveLast {
		elapsed := receivedMS - last
		if elapsed < 0 {
			// 시계 역행 — elapsed 0 으로 clamp (설계서 §7.3)
			res.ClockBackward = true
			elapsed = 0
		}
		if elapsed < debounce.Milliseconds() {
			// drop 이어도 시계 역행 여부는 알린다.
			if err := tx.Commit(); err != nil {
				return AdmitResult{}, err
			}
			return res, nil
		}
	}

	ins, err := tx.Exec(`INSERT INTO queue_items
	  (reader_id, epc, checked_at, checked_at_unix_ms, enqueued_at_unix_ms, attempt_count, next_attempt_at_unix_ms)
	  VALUES(?, ?, ?, ?, ?, 0, ?)`,
		readerID, epc, checkedAt, receivedMS, receivedMS, receivedMS)
	if err != nil {
		return AdmitResult{}, err
	}
	id, err := ins.LastInsertId()
	if err != nil {
		return AdmitResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO debounce_state(reader_id, epc, last_accepted_at_unix_ms)
	  VALUES(?, ?, ?)
	  ON CONFLICT(reader_id, epc) DO UPDATE SET last_accepted_at_unix_ms=excluded.last_accepted_at_unix_ms`,
		readerID, epc, receivedMS); err != nil {
		return AdmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdmitResult{}, err
	}
	res.Accepted = true
	res.ItemID = id
	return res, nil
}

// NextDue 는 sendable 상태 reader 의 due 행 중 가장 이른 것 하나를 돌려준다 (설계서 §7.4).
func (s *Store) NextDue(nowMS int64, sendableReaders []string) (*domain.QueueItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(sendableReaders) == 0 {
		return nil, nil
	}
	args := []any{nowMS}
	ph := make([]string, len(sendableReaders))
	for i, r := range sendableReaders {
		ph[i] = "?"
		args = append(args, r)
	}
	q := fmt.Sprintf(`SELECT id, reader_id, epc, checked_at, checked_at_unix_ms, attempt_count, next_attempt_at_unix_ms
	  FROM queue_items
	  WHERE next_attempt_at_unix_ms <= ? AND reader_id IN (%s)
	  ORDER BY checked_at_unix_ms, id LIMIT 1`, strings.Join(ph, ","))
	var it domain.QueueItem
	err := s.db.QueryRow(q, args...).Scan(&it.ID, &it.ReaderID, &it.EPC, &it.CheckedAt,
		&it.CheckedAtUnixMS, &it.AttemptCount, &it.NextAttemptAtMS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// NextDueAtMS 는 sendable reader 의 가장 이른 next_attempt 시각이다 (타이머 계산용).
func (s *Store) NextDueAtMS(sendableReaders []string) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(sendableReaders) == 0 {
		return 0, false, nil
	}
	args := []any{}
	ph := make([]string, len(sendableReaders))
	for i, r := range sendableReaders {
		ph[i] = "?"
		args = append(args, r)
	}
	q := fmt.Sprintf(`SELECT MIN(next_attempt_at_unix_ms) FROM queue_items WHERE reader_id IN (%s)`,
		strings.Join(ph, ","))
	var v sql.NullInt64
	if err := s.db.QueryRow(q, args...).Scan(&v); err != nil {
		return 0, false, err
	}
	if !v.Valid {
		return 0, false, nil
	}
	return v.Int64, true, nil
}

// Complete 은 terminal 판정(성공·drop)으로 큐 행을 삭제한다 (불변식 1).
func (s *Store) Complete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM queue_items WHERE id=?`, id)
	return err
}

// RetryLater 는 실패 행을 미래로 미룬다. checkedAt 과 EPC 는 절대 바뀌지 않는다 (불변식 2).
func (s *Store) RetryLater(id int64, nextAttemptMS int64, errClass string, httpStatus int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE queue_items
	  SET attempt_count = attempt_count + 1, next_attempt_at_unix_ms = ?, last_error_class = ?, last_http_status = ?
	  WHERE id = ?`, nextAttemptMS, errClass, httpStatus, id)
	return err
}

// MarkAttemptKeepDue 는 HALT_GLOBAL 트리거 행처럼 시도 기록만 남기고 행을 보존한다.
func (s *Store) MarkAttemptKeepDue(id int64, errClass string, httpStatus int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE queue_items
	  SET attempt_count = attempt_count + 1, last_error_class = ?, last_http_status = ?
	  WHERE id = ?`, errClass, httpStatus, id)
	return err
}

// ExpiredItem 은 만료 로그용 최소 정보다 (EPC·원래 시각).
type ExpiredItem struct {
	ReaderID  string
	EPC       string
	CheckedAt string
}

// ExpireBefore 는 원래 checked_at 이 cutoff 이전인 행을 서버 호출 없이 삭제한다 (R6).
func (s *Store) ExpireBefore(cutoffMS int64) ([]ExpiredItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT reader_id, epc, checked_at FROM queue_items WHERE checked_at_unix_ms < ?`, cutoffMS)
	if err != nil {
		return nil, err
	}
	var out []ExpiredItem
	for rows.Next() {
		var it ExpiredItem
		if err := rows.Scan(&it.ReaderID, &it.EPC, &it.CheckedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, it)
	}
	rows.Close()
	if len(out) > 0 {
		if _, err := tx.Exec(`DELETE FROM queue_items WHERE checked_at_unix_ms < ?`, cutoffMS); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// CleanupDebounce 는 오래된 디바운스 행 중 pending 큐가 없는 것을 정리한다 (설계서 §7.3).
func (s *Store) CleanupDebounce(beforeMS int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM debounce_state
	  WHERE last_accepted_at_unix_ms < ?
	  AND NOT EXISTS (
	    SELECT 1 FROM queue_items q
	    WHERE q.reader_id = debounce_state.reader_id AND q.epc = debounce_state.epc
	  )`, beforeMS)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Depth 는 현재 큐 길이다.
func (s *Store) Depth() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM queue_items`).Scan(&n)
	return n, err
}

// OldestCheckedAt 은 만료 임박 경고·status 용이다.
func (s *Store) OldestCheckedAt() (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v sql.NullString
	err := s.db.QueryRow(`SELECT checked_at FROM queue_items ORDER BY checked_at_unix_ms LIMIT 1`).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.String, v.Valid, nil
}

// PendingCount 는 reader 별 대기 행 수다 (suspension·resume 안내용).
func (s *Store) PendingCount(readerID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM queue_items WHERE reader_id=?`, readerID).Scan(&n)
	return n, err
}

// DiscardPending 은 resume --pending discard 용이다.
func (s *Store) DiscardPending(readerID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM queue_items WHERE reader_id=?`, readerID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GateRow 는 영속화된 gate 상태다. 토큰 원문은 저장하지 않는다 — fingerprint 만 (설계서 §7.2).
type GateRow struct {
	ReaderID         string
	State            domain.GateState
	Reason           string
	ChangedAtUnixMS  int64
	TokenFingerprint string
	Meta             domain.GateMeta
}

// SetGate 는 gate 상태를 영속화한다. meta 가 nil 이면 기존 meta 를 유지한다.
func (s *Store) SetGate(readerID string, state domain.GateState, reason string, nowMS int64, fingerprint string, meta *domain.GateMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if meta != nil {
		_, err := s.db.Exec(`INSERT INTO gate_state
		  (reader_id, state, reason, changed_at_unix_ms, token_fingerprint, event_name, booth_name, unit_name, cooldown_sec)
		  VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		  ON CONFLICT(reader_id) DO UPDATE SET
		    state=excluded.state, reason=excluded.reason, changed_at_unix_ms=excluded.changed_at_unix_ms,
		    token_fingerprint=excluded.token_fingerprint, event_name=excluded.event_name,
		    booth_name=excluded.booth_name, unit_name=excluded.unit_name, cooldown_sec=excluded.cooldown_sec`,
			readerID, string(state), reason, nowMS, fingerprint,
			meta.EventName, meta.BoothName, meta.UnitName, meta.CooldownSec)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO gate_state
	  (reader_id, state, reason, changed_at_unix_ms, token_fingerprint)
	  VALUES(?, ?, ?, ?, ?)
	  ON CONFLICT(reader_id) DO UPDATE SET
	    state=excluded.state, reason=excluded.reason, changed_at_unix_ms=excluded.changed_at_unix_ms,
	    token_fingerprint=excluded.token_fingerprint`,
		readerID, string(state), reason, nowMS, fingerprint)
	return err
}

// Gates 는 영속화된 gate 상태 전체다.
func (s *Store) Gates() (map[string]GateRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT reader_id, state, reason, changed_at_unix_ms, token_fingerprint,
	  event_name, booth_name, unit_name, cooldown_sec FROM gate_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]GateRow{}
	for rows.Next() {
		var g GateRow
		var st string
		if err := rows.Scan(&g.ReaderID, &st, &g.Reason, &g.ChangedAtUnixMS, &g.TokenFingerprint,
			&g.Meta.EventName, &g.Meta.BoothName, &g.Meta.UnitName, &g.Meta.CooldownSec); err != nil {
			return nil, err
		}
		g.State = domain.GateState(st)
		out[g.ReaderID] = g
	}
	return out, rows.Err()
}
