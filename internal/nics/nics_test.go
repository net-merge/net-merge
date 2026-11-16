package nics

import (
	"net"
	"testing"
)

func TestLinkCountersReadRealAdapter(t *testing.T) {
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if _, _, ok := Octets(ifc.Index); !ok {
			t.Fatalf("could not read OS counters for %s (index %d)", ifc.Name, ifc.Index)
		}
		return
	}
	t.Skip("no up adapter")
}

func TestIsPhoneFindsAnIPhoneHotspot(t *testing.T) {
	for _, c := range []struct {
		ip, desc string
		want     bool
	}{
		{"172.20.10.2", "", true},                  // Personal Hotspot over Wi-Fi: only the address tells
		{"172.20.10.14", "Wi-Fi", true},            // the last address of the /28
		{"", "iPhone USB", true},                   // macOS, before DHCP
		{"", "Apple Mobile Device Ethernet", true}, // Windows
		{"", "ipheth", true},                       // Linux driver
		{"172.20.10.16", "Wi-Fi", false},           // next door to the /28
		{"192.168.1.20", "Wi-Fi", false},
		{"172.20.11.2", "Ethernet", false},
		{"10.0.0.5", "r8169", false},
		{"", "", false},
	} {
		if got := isPhone(net.ParseIP(c.ip).To4(), c.desc); got != c.want {
			t.Errorf("isPhone(%q, %q) = %v, want %v", c.ip, c.desc, got, c.want)
		}
	}
}
