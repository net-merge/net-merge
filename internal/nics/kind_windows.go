package nics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// AdapterKinds reads Windows' interface type, without changing any adapter.
func AdapterKinds() map[string]Kind {
	size := uint32(32 << 10)
	for range 4 {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, aa, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil
		}
		out := map[string]Kind{}
		for a := aa; a != nil; a = a.Next {
			out[windows.UTF16PtrToString(a.FriendlyName)] = windowsKind(a.IfType, windows.UTF16PtrToString(a.Description))
		}
		return out
	}
	return nil
}

func windowsKind(ifType uint32, description string) Kind {
	switch {
	case phoneDescription(description) || ifType == 243 || ifType == 244:
		return Phone
	case ifType == 71:
		return WiFi
	case ifType == 6 || ifType == 62 || ifType == 69 || ifType == 117:
		return Ethernet
	default:
		return Unknown
	}
}
