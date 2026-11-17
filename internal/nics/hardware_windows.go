package nics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Hardware includes disabled physical adapters so the flyout can enable one again.
type Hardware struct {
	Name              string
	Index             int
	Enabled, Wireless bool
}

func HardwareAdapters() ([]Hardware, error) {
	size := uint32(32 << 10)
	for range 4 {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, aa, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, err
		}
		var out []Hardware
		for a := aa; a != nil; a = a.Next {
			name := windows.UTF16PtrToString(a.FriendlyName)
			if !physicalIfType(a.IfType) || virtualName.MatchString(name) || virtualDesc.MatchString(windows.UTF16PtrToString(a.Description)) {
				continue
			}
			idx := a.IfIndex
			if idx == 0 {
				idx = a.Ipv6IfIndex
			}
			row := windows.MibIfRow2{InterfaceIndex: idx}
			if windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) != nil {
				continue
			}
			out = append(out, Hardware{Name: name, Index: int(idx), Enabled: row.AdminStatus == 1, Wireless: a.IfType == 71})
		}
		return out, nil
	}
	return nil, windows.ERROR_BUFFER_OVERFLOW
}
