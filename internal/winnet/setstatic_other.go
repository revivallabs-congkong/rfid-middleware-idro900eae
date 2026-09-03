//go:build !windows

package winnet

import "errors"

var errWindowsOnly = errors.New("windows 전용 기능입니다")

// SetStatic 은 비 Windows 개발 환경에서는 지원하지 않는다.
func SetStatic(name, ip, mask string) error { return errWindowsOnly }

// SetDHCP 은 비 Windows 개발 환경에서는 지원하지 않는다.
func SetDHCP(name string) error { return errWindowsOnly }
