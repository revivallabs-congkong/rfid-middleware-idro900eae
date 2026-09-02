package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
)

// SaveReport 는 마법사 결과를 사람용 .txt + 기계용 .json 으로 저장한다
// (GUI 설계 §5.4). 직렬화 전 logging.Redact 을 거쳐 토큰·EPC 전문 부재를
// 보장한다. 저장 경로를 돌려준다.
func SaveReport(dataDir, version, cfgFingerprint string, st WizardState) (string, error) {
	reportsDir := filepath.Join(dataDir, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-1504")
	base := filepath.Join(reportsDir, "field-check-"+stamp)

	// 기계용 JSON — Redact 통과
	steps := make([]map[string]any, 0, len(st.Steps))
	for _, s := range st.Steps {
		f := logging.F{"id": s.ID, "title": s.Title, "status": string(s.Status), "detail": s.Detail}
		if s.Action != "" {
			f["action"] = s.Action
		}
		m := map[string]any{}
		for k, v := range logging.Redact(f) {
			m[k] = v
		}
		if len(s.Metrics) > 0 {
			// metrics 는 원래 키(epc 등)를 유지해 redact — 키가 바뀌면 epc 마스킹을 놓친다
			mr := map[string]any{}
			for k, v := range logging.Redact(metricsToF(s.Metrics)) {
				mr[k] = v
			}
			m["metrics"] = mr
		}
		steps = append(steps, m)
	}
	jsonReport := map[string]any{
		"version": version, "cfgFingerprint": cfgFingerprint,
		"startedAt": st.StartedAt, "finishedAt": st.FinishedAt, "steps": steps,
	}
	jb, _ := json.MarshalIndent(jsonReport, "", "  ")
	if err := os.WriteFile(base+".json", jb, 0o600); err != nil {
		return "", err
	}

	// 사람용 TXT
	var b strings.Builder
	fmt.Fprintf(&b, "CongKong RFID 현장 점검 리포트\n")
	fmt.Fprintf(&b, "프로그램: %s · 설정: %s\n", version, cfgFingerprint)
	fmt.Fprintf(&b, "시작: %s · 종료: %s\n\n", st.StartedAt, st.FinishedAt)
	for _, s := range st.Steps {
		mark := map[StepStatus]string{
			StepPass: "[통과]", StepWarn: "[주의]", StepFail: "[실패]", StepSkip: "[건너뜀]",
		}[s.Status]
		fmt.Fprintf(&b, "%s %s — %s\n", mark, s.Title, logging.RedactString(s.Detail))
		if s.Action != "" {
			fmt.Fprintf(&b, "        👉 %s\n", logging.RedactString(s.Action))
		}
		// metrics 도 redact 경유
		if len(s.Metrics) > 0 {
			red := logging.Redact(metricsToF(s.Metrics))
			var parts []string
			for k, v := range red {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			fmt.Fprintf(&b, "        %s\n", strings.Join(parts, " "))
		}
	}
	if err := os.WriteFile(base+".txt", []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return base + ".txt", nil
}

func metricsToF(m map[string]string) logging.F {
	f := logging.F{}
	for k, v := range m {
		f[k] = v
	}
	return f
}
