package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/config"
)

// WriteReaderSession 은 config.json 의 특정 리더에 세션(토큰)을 반영한다
// (GUI 설계 §6.8): 백업 → map 치환 → 재검증 → temp+fsync+rename.
// 성공 시 .bak 경로를 돌려준다 — 재기동 성공 후 RemoveBackup, 실패 시 Rollback.
func WriteReaderSession(cfgPath, readerID, sessionID, token string) (bak string, err error) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("설정 읽기 실패: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("설정 파싱 실패: %w", err)
	}
	readers, _ := m["readers"].([]any)
	found := false
	for _, ri := range readers {
		r, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		if r["id"] == readerID {
			r["pulseToken"] = token
			r["sessionId"] = sessionID
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("reader %q 가 설정에 없음", readerID)
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	// 쓰기 전에 코어가 읽을 수 있는 내용인지 재검증 (§6.8)
	if _, err := config.Parse(bytesReader(out)); err != nil {
		return "", fmt.Errorf("생성된 설정이 검증 실패: %w", err)
	}

	bak = cfgPath + ".bak"
	if err := os.WriteFile(bak, raw, 0o600); err != nil {
		return "", fmt.Errorf("백업 실패: %w", err)
	}
	tmp := cfgPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return bak, nil
}

// RollbackConfig 는 .bak 을 원위치로 되돌린다 (재기동 실패 시 — §3.4).
func RollbackConfig(cfgPath, bak string) error {
	raw, err := os.ReadFile(bak)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return err
	}
	return os.Remove(bak)
}

// RemoveBackup 은 적용 성공 후 백업을 지운다.
func RemoveBackup(bak string) { os.Remove(bak) }

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
