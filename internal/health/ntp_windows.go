//go:build windows

package health

import (
	"os/exec"
	"strings"
)

// NTPStatus 는 기동 시 시계 신뢰성 점검 결과다 (설계서 §12.1).
type NTPStatus struct {
	Supported      bool
	ServiceRunning bool
	QueryOK        bool
	Detail         string
}

// CheckNTP 는 Windows Time 서비스 상태와 w32tm 조회 성공 여부를 확인한다.
// 로케일 의존 텍스트는 핵심 판정에 사용하지 않는다 — 실행 성공 여부만 본다.
func CheckNTP() NTPStatus {
	st := NTPStatus{Supported: true}

	out, err := exec.Command("sc", "query", "w32time").CombinedOutput()
	if err == nil && strings.Contains(strings.ToUpper(string(out)), "RUNNING") {
		st.ServiceRunning = true
	}

	if err := exec.Command("w32tm", "/query", "/status").Run(); err == nil {
		st.QueryOK = true
	} else {
		st.Detail = "w32tm /query /status 실패"
	}
	return st
}
