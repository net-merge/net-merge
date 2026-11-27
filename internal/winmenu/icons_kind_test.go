package winmenu

import "testing"

const lrm = leftToRight

// The rows that the other tray changes added must have icons like the rest of the menu.
func TestNewMenuItemsHaveIcons(t *testing.T) {
	for _, c := range []struct{ title, want string }{
		{"Look up ISP names", iconProvider},
		{"Game mode", iconGame},
		{"Cool down phone", iconCool},
	} {
		if got := iconFor(c.title, false); got != c.want || got == "" {
			t.Errorf("iconFor(%q) = %q, want %q", c.title, got, c.want)
		}
	}
	icons := map[string]string{}
	for _, title := range []string{
		"Look up ISP names", "Game mode", "Cool down phone",
		"Use all adapters", "Auto-pause",
		"Start at sign-in", "Start merging", "Quit",
	} {
		g := iconFor(title, false)
		if other, dup := icons[g]; dup {
			t.Errorf("%q and %q share an icon", title, other)
		}
		icons[g] = title
	}
}

// The adapter's type, when the system says it, wins over a guess from its name; "network" (unknown)
// leaves the guess in charge.
func TestKnownAdapterKindWinsOverItsName(t *testing.T) {
	for _, c := range []struct{ title, kind, want string }{
		{"Home" + lrm + "  ↓1 ↑1 Mbps", "wifi", iconWifi},
		{"Home" + lrm + "  ↓1 ↑1 Mbps", "ethernet", iconEthernet},
		{"Home" + lrm + "  ↓1 ↑1 Mbps", "phone", iconPhone},
		{"Home" + lrm + "  ↓1 ↑1 Mbps", "network", iconNetwork},
		{"Home" + lrm + "  ↓1 ↑1 Mbps", "", iconNetwork},
		{"Wi-Fi" + lrm + "  ↓1 ↑1 Mbps", "ethernet", iconEthernet}, // the system's word, not the name's
		{"Bluetooth Network" + lrm + "  ↓1 ↑1 Mbps", "network", iconBT},
	} {
		if got := iconForItem(c.title, true, c.kind); got != c.want {
			t.Errorf("iconForItem(%q, kind %q) = %q, want %q", c.title, c.kind, got, c.want)
		}
	}
}

// An ISP name, an alias or the speed text after the adapter's name must never pick the icon.
func TestAdapterRowTextAfterTheNameDoesNotPickTheIcon(t *testing.T) {
	for _, c := range []struct{ title, want string }{
		{"Wi-Fi" + lrm + "  ↓79 ↑7.0 Mbps  (31 flows via net-merge)  +IPv6", iconWifi},
		{lrm + "Wi-Fi · Planet Online" + lrm + "  ↓1 ↑1 Mbps", iconWifi},         // "lan" is in Planet
		{lrm + "Home · Sprint Wireless USB" + lrm + "  ↓1 ↑1 Mbps", iconNetwork}, // not Wi-Fi, not USB
		{"Ethernet 3:  ↓16 ↑0.2 Mbps  wifi", iconEthernet},                       // the older format
		{"Ethernet 5" + lrm + "  ↓1 ↑1 Mbps  limit 20 Mbit/s  cooling at 20 Mbit/s", iconEthernet},
	} {
		if got := iconFor(c.title, true); got != c.want {
			t.Errorf("iconFor(%q) = %q, want %q", c.title, got, c.want)
		}
	}
	for _, c := range []struct{ title, want string }{
		{lrm + "Wi-Fi · Planet Online" + lrm + "  ↓1 ↑1 Mbps", "Wi-Fi"},
		{"Ethernet 3:  ↓16 ↑0.2 Mbps", "Ethernet 3"},
		{"No speed yet", "No speed yet"},
	} {
		if got := adapterName(c.title); got != c.want && got != c.want+" " {
			t.Errorf("adapterName(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestVersionRowAcceptsBuildsBetweenReleases(t *testing.T) {
	for _, c := range []struct {
		s    string
		want bool
	}{
		{"v0.3.7", true}, {"v0.3.7-5-g1a2b3c", true}, {"v1.0.0+build.4", true}, {"v10", true},
		{"v", false}, {"vlocal-menu-review", false}, {"0.3.7", false}, {"v0.3 7", false}, {"v0.3.7" + lrm, false},
	} {
		if got := isVersion(c.s); got != c.want {
			t.Errorf("isVersion(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestSnapshotUsesTheItemsAdapterKind(t *testing.T) {
	parent := newItem("Home"+lrm+"  ↓1 ↑1 Mbps", false)
	parent.kids = []*Item{newItem("Unlimited", false)}
	rows := snapshot([]*Item{parent})
	if len(rows) != 1 || rows[0].icon != iconNetwork {
		t.Fatalf("an adapter of no known kind: %+v", rows)
	}
	parent.SetAdapterKind("phone")
	if rows = snapshot([]*Item{parent}); rows[0].icon != iconPhone {
		t.Errorf("a phone's row has icon %q", rows[0].icon)
	}
	parent.SetAdapterKind("wifi")
	if rows = snapshot([]*Item{parent}); rows[0].icon != iconWifi {
		t.Errorf("a Wi-Fi row has icon %q", rows[0].icon)
	}
}

// Start installs the redraw callback while other goroutines may be changing items: it is atomic, and a
// change before Start (no menu thread yet) is simply ignored.
func TestChangeBeforeAndAfterStart(t *testing.T) {
	prev := onChanged.Load()
	defer onChanged.Store(prev)
	onChanged.Store(nil)
	it := newItem("x", false)
	it.Check() // no menu thread: must not panic
	n := 0
	f := func() { n++ }
	onChanged.Store(&f)
	it.Uncheck()
	it.SetTitle("y")
	if n != 2 {
		t.Errorf("the redraw callback ran %d times, want 2", n)
	}
}
