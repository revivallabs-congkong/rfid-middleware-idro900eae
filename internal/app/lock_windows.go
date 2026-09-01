//go:build windows

package app

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireLock 은 동일 data directory 의 이중 실행을 거부한다 (설계서 §7.1).
// Windows 에서는 LockFileEx 배타 잠금을 사용한다 — 프로세스 종료 시 OS 가 해제한다.
func acquireLock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	h := windows.Handle(f.Fd())
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol); err != nil {
		f.Close()
		return nil, fmt.Errorf("다른 인스턴스가 이미 실행 중입니다 (%s)", path)
	}
	return func() {
		windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
		os.Remove(path)
	}, nil
}
