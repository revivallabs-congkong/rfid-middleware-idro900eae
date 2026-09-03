// Package winnet 은 호스트 네트워크 자가 진단·설정 어댑터다.
// 리더는 DHCP 서버가 아니므로(장비 명세 2.2), 리더와 직결된 호스트는 APIPA
// (169.254.x) 를 받아 리더 대역(예: 192.168.9.x)과 통신하지 못한다. 이 패키지는
// "리더 대역에 IP 가 있는지" 를 진단하고, 없으면 원터치로 고정 IP 를 부여한다.
package winnet

import (
	"fmt"
	"net"
	"strings"
)

// Iface 는 호스트 네트워크 어댑터 1개의 요약이다.
type Iface struct {
	Name string   `json:"name"`
	IPv4 []string `json:"ipv4"`
	Up   bool     `json:"up"`
}

// Subnet24 는 "host:port" 또는 "host" 에서 /24 프리픽스("192.168.9.")를 뽑는다.
// IPv4 가 아니면 "" 를 돌려준다.
func Subnet24(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	o := ip.To4()
	return fmt.Sprintf("%d.%d.%d.", o[0], o[1], o[2])
}

// List 는 loopback 을 제외한 어댑터 목록과 각 IPv4 를 돌려준다.
func List() []Iface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Iface
	for _, in := range ifs {
		if in.Flags&net.FlagLoopback != 0 {
			continue
		}
		e := Iface{Name: in.Name, Up: in.Flags&net.FlagUp != 0}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				e.IPv4 = append(e.IPv4, ipn.IP.To4().String())
			}
		}
		out = append(out, e)
	}
	return out
}

// HasHostInSubnet 은 어떤 어댑터든 주어진 /24 프리픽스의 IPv4 를 가졌는지다.
func HasHostInSubnet(prefix string) bool {
	if prefix == "" {
		return false
	}
	for _, f := range List() {
		for _, ip := range f.IPv4 {
			if strings.HasPrefix(ip, prefix) {
				return true
			}
		}
	}
	return false
}

// SubnetIP 는 리더 대역에 이미 있는 호스트 IP 를 돌려준다(없으면 "").
func SubnetIP(prefix string) string {
	if prefix == "" {
		return ""
	}
	for _, f := range List() {
		for _, ip := range f.IPv4 {
			if strings.HasPrefix(ip, prefix) {
				return ip
			}
		}
	}
	return ""
}

// isAPIPA 는 169.254.x (자동 사설 IP — DHCP 실패)인지다.
func isAPIPA(ip string) bool { return strings.HasPrefix(ip, "169.254.") }

// Candidates 는 고정 IP 를 안전하게 부여할 수 있는 어댑터 이름 목록이다.
// 기준: 물리 NIC(하드웨어 MAC 존재) 이고, Up 이며, 라우팅 가능한 사설 IPv4 가
// 없다(= IP 가 없거나 APIPA 뿐). 이래야 인터넷을 제공 중인 Wi-Fi/DHCP 어댑터나
// 터널·가상 어댑터(utun/awdl 등, MAC 없음)를 건드리지 않는다.
func Candidates(readerPrefix string) []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, in := range ifs {
		if in.Flags&net.FlagLoopback != 0 || in.Flags&net.FlagUp == 0 {
			continue
		}
		if len(in.HardwareAddr) == 0 {
			continue // 가상/터널 어댑터 제외
		}
		routable := false
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !isAPIPA(ipn.IP.To4().String()) {
				routable = true
				break
			}
		}
		if !routable {
			out = append(out, in.Name)
		}
	}
	return out
}

// HostIPFor 는 리더 대역에서 호스트에 줄 IP 를 고른다. 리더 IP 의 마지막
// 옥텟과 게이트웨이(.1)를 피해 .100(충돌 시 .101)을 쓴다 (장비 명세 2.2 권장).
func HostIPFor(readerAddr string) string {
	prefix := Subnet24(readerAddr)
	if prefix == "" {
		return ""
	}
	last := ""
	host := readerAddr
	if h, _, err := net.SplitHostPort(readerAddr); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		o := ip.To4()
		last = fmt.Sprintf("%d", o[3])
	}
	if last == "100" {
		return prefix + "101"
	}
	return prefix + "100"
}
