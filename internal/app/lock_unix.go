//go:build !windows

package app

import (
	"fmt"
	"os"
	"syscall"
)

// acquireLock 은 동일 data directory 의 이중 실행을 거부한다 (설계서 §7.1).
func acquireLock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("다른 인스턴스가 이미 실행 중입니다 (%s)", path)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		os.Remove(path)
	}, nil
}
