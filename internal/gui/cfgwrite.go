package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

// ConfigEdit 은 설정 UI 가 편집하는 필드다. 토큰은 여기서 다루지 않는다 —
// 토큰은 세션 선택으로만 들어간다 (GUI 설계 §3, plan §2.2). 기존 리더의
// 토큰·sessionId 는 보존한다.
type ConfigEdit struct {
	APIHost           string       `json:"apiHost"`
	DataDir           string       `json:"dataDir"`
	DebounceSec       int          `json:"debounceSec"`
	QueueMaxAgeHours  int          `json:"queueMaxAgeHours"`
	RequestTimeoutSec int          `json:"requestTimeoutSec"`
	PowerGain         int          `json:"powerGain"`
	Buzzer            int          `json:"buzzer"`
	LogLevel          string       `json:"logLevel"`
	SessionsFile      string       `json:"sessionsFile"`
	Readers           []ReaderEdit `json:"readers"`
}

type ReaderEdit struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// WriteConfig 는 설정 UI 의 편집을 반영한다: 기존 파일에서 리더 토큰을
// 보존하며 전역 필드·리더 목록(addr)을 교체한다. 신규 리더는 빈 토큰(0×64)
// placeholder 로 만들어 이후 세션 선택으로 채운다. 백업→검증→원자적 쓰기.
func WriteConfig(cfgPath string, e ConfigEdit) (bak string, err error) {
	raw, _ := os.ReadFile(cfgPath) // 없으면 신규 생성
	oldTokens := map[string][2]string{}
	if len(raw) > 0 {
		var old map[string]any
		if json.Unmarshal(raw, &old) == nil {
			if rs, ok := old["readers"].([]any); ok {
				for _, ri := range rs {
					if r, ok := ri.(map[string]any); ok {
						id, _ := r["id"].(string)
						tok, _ := r["pulseToken"].(string)
						sid, _ := r["sessionId"].(string)
						oldTokens[id] = [2]string{tok, sid}
					}
				}
			}
		}
	}

	m := map[string]any{
		"version": 1, "apiHost": e.APIHost, "dataDir": e.DataDir,
		"debounceSec": e.DebounceSec, "queueMaxAgeHours": e.QueueMaxAgeHours,
		"requestTimeoutSec": e.RequestTimeoutSec, "powerGain": e.PowerGain,
		"buzzer": e.Buzzer, "logLevel": e.LogLevel,
	}
	if e.SessionsFile != "" {
		m["sessionsFile"] = e.SessionsFile
	}
	var readers []map[string]any
	for _, r := range e.Readers {
		tok := "0000000000000000000000000000000000000000000000000000000000000000"
		sid := ""
		if prev, ok := oldTokens[r.ID]; ok {
			if prev[0] != "" {
				tok = prev[0]
			}
			sid = prev[1]
		}
		rm := map[string]any{"id": r.ID, "addr": r.Addr, "pulseToken": tok}
		if sid != "" {
			rm["sessionId"] = sid
		}
		readers = append(readers, rm)
	}
	m["readers"] = readers

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if _, err := config.Parse(bytesReader(out)); err != nil {
		return "", fmt.Errorf("설정이 검증을 통과하지 못했습니다: %w", err)
	}

	if len(raw) > 0 {
		bak = cfgPath + ".bak"
		if err := os.WriteFile(bak, raw, 0o600); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return "", err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
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
