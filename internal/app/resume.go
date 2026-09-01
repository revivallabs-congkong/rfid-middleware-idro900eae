package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/store/sqlite"
)

// QueueResume 은 suspended reader 를 재개한다 (설계서 §8.2, §10).
// 서비스 중지 상태에서만 실행된다 — app.lock 으로 강제한다.
// pending 은 "send"(보존 행 전송 재개) 또는 "discard"(폐기)다.
func QueueResume(cfg *config.Config, readerID, pending string) (string, error) {
	if pending != "send" && pending != "discard" {
		return "", fmt.Errorf(`--pending 은 send 또는 discard 여야 합니다`)
	}
	if _, ok := cfg.Reader(readerID); !ok {
		return "", fmt.Errorf("reader %q 가 설정에 없습니다", readerID)
	}

	release, err := acquireLock(filepath.Join(cfg.DataDir, "app.lock"))
	if err != nil {
		return "", fmt.Errorf("서비스를 먼저 중지하세요: %w", err)
	}
	defer release()

	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "queue.db"))
	if err != nil {
		return "", err
	}
	defer st.Close()

	gates, err := st.Gates()
	if err != nil {
		return "", err
	}
	row, ok := gates[readerID]
	if !ok || !row.State.Suspended() {
		return "", fmt.Errorf("reader %q 는 suspended 상태가 아닙니다 (현재: %s)", readerID, row.State)
	}

	count, err := st.PendingCount(readerID)
	if err != nil {
		return "", err
	}
	discarded := int64(0)
	if pending == "discard" {
		discarded, err = st.DiscardPending(readerID)
		if err != nil {
			return "", err
		}
	}
	// PREFLIGHT_PENDING 으로 되돌린다 — 다음 기동 preflight 가 새 토큰을 검증한다.
	if err := st.SetGate(readerID, domain.GatePreflightPending, "operator resume",
		time.Now().UnixMilli(), row.TokenFingerprint, nil); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("reader %s 재개 준비 완료 (이전 상태: %s, 대기 행 %d건", readerID, row.State, count)
	if pending == "discard" {
		msg += fmt.Sprintf(" 중 %d건 폐기", discarded)
	} else {
		msg += " 전송 예정"
	}
	msg += "). 서비스를 시작하면 preflight 후 재개됩니다."
	return msg, nil
}
