// Package health 는 NTP/디스크/status snapshot 을 담당한다 (설계서 §12).
package health

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StatusSchema 는 status.json 스키마 버전이다 (GUI 설계 §6.1 — v2).
// 마커 없는(0) 파일은 구버전(v1)으로 본다.
const StatusSchema = 2

// Status 는 status.json 스냅샷이다. attendee 와 EPC, 토큰은 넣지 않는다 (설계서 §12.3).
type Status struct {
	Schema             int            `json:"schema,omitempty"`
	UpdatedAt          string         `json:"updatedAt"`
	SenderState        string         `json:"senderState"`
	QueueDepth         int64          `json:"queueDepth"`
	QueueNonEmptySince string         `json:"queueNonEmptySince,omitempty"`
	OldestCheckedAt    string         `json:"oldestCheckedAt,omitempty"`
	PID                int            `json:"pid,omitempty"`
	Version            string         `json:"version,omitempty"`
	Mode               string         `json:"mode,omitempty"` // service|hosting|cli
	StartedAt          string         `json:"startedAt,omitempty"`
	NTP                *NTPInfo       `json:"ntp,omitempty"`
	Server             *ServerInfo    `json:"server,omitempty"`
	SuccessSinceStart  int64          `json:"successSinceStart"`
	Readers            []ReaderStatus `json:"readers"`
}

// ServerInfo 는 콩콩 서버 도달성(인터넷 연결) 진단이다. Seen=false 면 아직
// 통신 시도 전(판단 불가).
type ServerInfo struct {
	Seen   bool   `json:"seen"`
	Online bool   `json:"online"`
	LastOK string `json:"lastOk,omitempty"`
}

// NTPInfo 는 시계 신뢰 진단이다. SkewSec 은 HTTP Date 기반 관측값 (GUI 설계 §6.9).
type NTPInfo struct {
	Checked bool   `json:"checked"`
	SkewSec int    `json:"skewSec"`
	At      string `json:"at,omitempty"`
}

type ReaderStatus struct {
	ID            string `json:"id"`
	GateState     string `json:"gateState"`
	GateReason    string `json:"gateReason,omitempty"`
	EventName     string `json:"eventName,omitempty"`
	BoothName     string `json:"boothName,omitempty"`
	UnitName      string `json:"unitName,omitempty"`
	CooldownSec   int    `json:"cooldownSec,omitempty"`
	ConnState     string `json:"connState,omitempty"` // CONNECTED|DISCONNECTED
	ConnSince     string `json:"connSince,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	Pending       int64  `json:"pending"`
	LastTagAt     string `json:"lastTagAt,omitempty"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
}

// WriteStatus 는 temp write + fsync + rename 으로 부분 JSON 을 방지한다.
func WriteStatus(path string, s Status) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// ReadStatus 는 status 명령용이다.
func ReadStatus(path string) (Status, error) {
	var s Status
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("status.json 파싱 실패: %w", err)
	}
	return s, nil
}

// CheckDiskDir 는 데이터 디렉토리 쓰기 가능 여부를 확인한다.
func CheckDiskDir(dir string) error {
	probe := filepath.Join(dir, ".disk-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return err
	}
	return os.Remove(probe)
}
