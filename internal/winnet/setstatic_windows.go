//go:build windows

package winnet

import (
	"fmt"
	"os/exec"
	"strings"
)

// SetStatic 은 named 어댑터에 고정 IPv4/mask 를 부여한다 (netsh, 관리자 권한).
// 게이트웨이는 지정하지 않는다 — 리더 전용 세그먼트이며 인터넷은 다른 어댑터
// (Wi-Fi 등)로 나가야 하기 때문이다.
func SetStatic(name, ip, mask string) error {
	// name 에 공백("이더넷 2")이 있어도 exec 는 argv 단위라 그대로 전달하면 된다.
	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		"name="+name, "static", ip, mask)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh 실패: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// SetDHCP 는 어댑터를 DHCP 로 되돌린다 (원복용, 관리자 권한).
func SetDHCP(name string) error {
	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		"name="+name, "dhcp")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh 실패: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
