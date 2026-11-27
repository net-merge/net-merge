package winmenu

import "testing"

func TestIconFollowsTheEntryName(t *testing.T) {
	for _, c := range []struct {
		title  string
		hasSub bool
		want   string
	}{
		{"net-merge", false, iconNetwork},
		{"Merging  ·  Total ↓95 ↑7.2 Mbps", false, iconSpeed},
		{"Paused  ·  Total ↓0.0 ↑0.0 Mbps", false, iconPause},
		{"Paused: One Connection  ·  Total ↓1.0 ↑0.1 Mbps", false, iconPause},
		{"Error: could not start", false, iconWarning},
		{"Turning On...", false, iconSync},
		{"Use All Adapters", false, iconGlobe},
		{"Auto-Pause", false, iconLink},
		{"Start at Sign-In", false, iconPerson},
		{"Start Merging", false, iconPlay},
		{"Pause Merging", false, iconPause},
		{"v0.3.6", false, iconInfo},
		{"dev", false, iconInfo},
		{"Update to v0.3.7", false, iconDownload},
		{"Quit", false, iconPower},

		{"Wi-Fi:  ↓79 ↑7.0 Mbps  (31 flows via net-merge)  +IPv6", true, iconWifi},
		{"WLAN 2:  ↓1 ↑1 Mbps", true, iconWifi},
		{"Ethernet 3:  ↓16 ↑0.2 Mbps", true, iconEthernet},
		{"Local Area Connection:  ↓1 ↑1 Mbps", true, iconEthernet},
		{"iPhone USB:  ↓1 ↑1 Mbps", true, iconPhone},
		{"Cellular:  ↓1 ↑1 Mbps", true, iconPhone},
		{"Bluetooth Network:  ↓1 ↑1 Mbps", true, iconBT},
		{"USB Adapter:  ↓1 ↑1 Mbps", true, iconUSB},
		{"Home:  ↓1 ↑1 Mbps", true, iconNetwork}, // a name that says nothing still gets an icon

		// The speed-limit submenu: the check mark keeps the left column.
		{"Unlimited", false, ""},
		{"10 Mbit/s", false, ""},
	} {
		if got := iconFor(c.title, c.hasSub); got != c.want {
			t.Errorf("iconFor(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// What an adapter row says after its name must not change its icon.
func TestAdapterIconIgnoresTheSpeedText(t *testing.T) {
	if got := iconFor("Home:  ↓1 ↑1 Mbps  (2 flows via net-merge)  - no internet  wifi", true); got != iconNetwork {
		t.Errorf("got %q", got)
	}
}

func TestCheckedRowWithAnIconReservesRoomForTheCheckMark(t *testing.T) {
	plain := []row{{item: &Item{}, title: "Quit", icon: iconPower}}
	toggle := []row{{item: &Item{}, title: "Quit", icon: iconPower, checkable: true}}
	// Long enough that the width isn't just the menu's minimum.
	long := "a long enough title to pass the minimum width of the menu"
	plain[0].title, toggle[0].title = long, long
	w1, _ := layoutRows(plain, 96, 0, 0, fixedWidth(96))
	w2, _ := layoutRows(toggle, 96, 0, 0, fixedWidth(96))
	if w2 <= w1 {
		t.Errorf("toggle row is %d wide, plain row %d: no room for a check mark", w2, w1)
	}
}
