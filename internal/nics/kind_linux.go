package nics

import (
	"net"
	"os"
	"path/filepath"
)

func AdapterKinds() map[string]Kind {
	ifaces, _ := net.Interfaces()
	out := map[string]Kind{}
	for _, ifc := range ifaces {
		out[ifc.Name] = linuxKind(filepath.Join("/sys/class/net", ifc.Name))
	}
	return out
}

func linuxKind(path string) Kind {
	driver, _ := os.Readlink(filepath.Join(path, "device", "driver"))
	if phoneDescription(filepath.Base(driver)) {
		return Phone
	}
	if _, err := os.Stat(filepath.Join(path, "wireless")); err == nil {
		return WiFi
	}
	// ARPHRD_ETHER is also used by virtual devices, which keep the generic icon.
	if _, err := os.Stat(filepath.Join(path, "device")); err == nil {
		if data, err := os.ReadFile(filepath.Join(path, "type")); err == nil && string(data) == "1\n" {
			return Ethernet
		}
	}
	return Unknown
}
