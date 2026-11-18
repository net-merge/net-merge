package nics

import "testing"

func TestWindowsAdapterPresentationTypes(t *testing.T) {
	for _, tc := range []struct {
		ifType uint32
		desc   string
		want   Kind
	}{
		{71, "Intel Wi-Fi", WiFi}, {6, "Realtek GbE", Ethernet},
		{6, "Apple Mobile Device Ethernet", Phone}, {6, "Remote NDIS Internet Sharing Device", Phone},
		{243, "Mobile broadband", Phone}, {131, "Tunnel", Unknown},
	} {
		if got := windowsKind(tc.ifType, tc.desc); got != tc.want {
			t.Errorf("%d %q: %s, want %s", tc.ifType, tc.desc, got, tc.want)
		}
	}
}
