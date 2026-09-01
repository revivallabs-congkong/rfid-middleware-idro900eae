//go:build !windows

package health

// NTPStatus 는 기동 시 시계 신뢰성 점검 결과다 (설계서 §12.1).
type NTPStatus struct {
	Supported      bool
	ServiceRunning bool
	QueryOK        bool
	Detail         string
}

// CheckNTP 는 Windows 전용이다. 다른 OS(개발 환경)에서는 판정하지 않는다.
func CheckNTP() NTPStatus {
	return NTPStatus{Supported: false, Detail: "windows 전용 점검 — 이 OS 에서는 생략"}
}
