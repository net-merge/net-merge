package nics

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Nic is one physical adapter with the source addresses flows are pinned to: its IPv4 address
// and/or its global IPv6 address (either may be nil).
type Nic struct {
	Name   string
	Index  int // interface index, used to force egress (IP_UNICAST_IF); 0 = don't force
	IP     net.IP
	IP6    net.IP
	Weight float64
	Phone  bool // a phone sharing its mobile data: the scheduler goes easy on it (see engine/phone.go)
}

// Skip is an adapter that was seen but not used, with the reason (shown by `net-merge list`).
type Skip struct{ Name, Reason string }

// Second line of defence after the adapter type check: virtual adapters that report as plain
// Ethernet (virtual switches, VPN TAP drivers, VM host-only adapters, container bridges), matched
// by name or driver description. The anchored names are Linux interface naming conventions.
var (
	virtualName = regexp.MustCompile(`(?i)loopback|vethernet|wsl|tailscale|zerotier|wintun|net-merge|docker|podman|^veth|^virbr|^br-|^vboxnet|^vmnet|^vnet\d|^lxc|^lxdbr|^cni|^flannel|^wg\d|^tun\d|^tap\d|^zt`)
	virtualDesc = regexp.MustCompile(`(?i)virtual|vpn|\btap\b|tap-|tunnel|wintun|wireguard|tailscale|zerotier|openvpn|anyconnect|fortinet|vmware|npcap|miniport|teredo|isatap|6to4|pseudo`)
)

// An iPhone or iPad sharing its connection. Personal Hotspot always hands out 172.20.10.0/28, over
// USB, Wi-Fi and Bluetooth alike, so the address finds it on every OS and with -include (which
// skips the adapter details). The description catches a tether that has no IPv4 address yet:
// "iPhone USB" on macOS, "Apple Mobile Device Ethernet" on Windows, the ipheth driver on Linux.
var (
	iosHotspot = net.IPNet{IP: net.IPv4(172, 20, 10, 0).To4(), Mask: net.CIDRMask(28, 32)}
	iosDesc    = regexp.MustCompile(`(?i)iphone|ipad|apple mobile device|^ipheth$`)
)

func isPhone(ip4 net.IP, desc string) bool {
	return (ip4 != nil && iosHotspot.Contains(ip4)) || iosDesc.MatchString(desc)
}

func DiscoverNics(include []string, weights map[string]float64) ([]Nic, error) {
	nics, _, err := DiscoverNicsDetailed(include, weights)
	return nics, err
}

func DiscoverNicsDetailed(include []string, weights map[string]float64) ([]Nic, []Skip, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	// Auto mode only takes real hardware adapters that have a default gateway (can reach the
	// internet), per family. An explicit -include list overrides all of these checks.
	var info map[int]adapterInfo
	if len(include) == 0 {
		info, _ = adapterInfos() // nil on error: fall back to name filtering only
	}
	var out []Nic
	var skipped []Skip
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		skip := func(reason string) { skipped = append(skipped, Skip{ifc.Name, reason}) }
		ai, haveInfo := info[ifc.Index]
		if len(include) > 0 {
			if !containsFold(include, ifc.Name) {
				continue
			}
		} else {
			switch {
			case virtualName.MatchString(ifc.Name):
				skip("looks like a virtual/VPN adapter (name)")
				continue
			case info != nil && haveInfo && ai.virtual != "":
				skip(ai.virtual)
				continue
			case info != nil && haveInfo && virtualDesc.MatchString(ai.desc):
				skip(fmt.Sprintf("virtual/VPN adapter (%s)", ai.desc))
				continue
			}
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		var ip4, ip6 net.IP
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipn.IP.To4(); v4 != nil {
				if ip4 == nil && !v4.IsLinkLocalUnicast() { // 169.254/16: no DHCP lease, no internet
					ip4 = v4
				}
			} else if ip6 == nil && ipn.IP.IsGlobalUnicast() && !ipn.IP.IsPrivate() { // skips fe80:: and ULA
				ip6 = ipn.IP
			}
		}
		phone := isPhone(ip4, ai.desc)
		if info != nil {
			if !ai.v4 {
				ip4 = nil
			}
			if !ai.v6 {
				ip6 = nil
			}
		}
		if ip4 == nil && ip6 == nil {
			if info != nil && !ai.v4 && !ai.v6 {
				skip("no default gateway (no internet route)")
			} else {
				skip("no usable IP address")
			}
			continue
		}
		w := 1.0
		if v, ok := weights[ifc.Name]; ok {
			w = v
		}
		out = append(out, Nic{Name: ifc.Name, Index: ifc.Index, IP: ip4, IP6: ip6, Weight: w, Phone: phone})
	}
	return out, skipped, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
