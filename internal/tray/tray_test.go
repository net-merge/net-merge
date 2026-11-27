package tray

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"net-merge/internal/engine"
	"net-merge/internal/state"
)

func TestShortenCountsUTF16AndKeepsRunesWhole(t *testing.T) {
	s := "net-merge ON  ↓12 ↑3.4 Mbit/s"
	if got := shorten(s, 127); got != s {
		t.Fatalf("short string changed: %q", got)
	}
	got := shorten(s, 16)
	if n := len(utf16.Encode([]rune(got))); n > 16 {
		t.Fatalf("%q is %d UTF-16 units, want <= 16", got, n)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("bad cut: %q", got)
	}
}

func TestAdapterRowKeepsLiveDetailsWithAliasAndISP(t *testing.T) {
	s := engine.SlotStats{Name: "Ethernet 5", LinkIn: 12.3 * 125000, LinkOut: 1.2 * 125000, State: "up", State6: "up", Active: 7, LimitMbps: 20}
	got := adapterRow(s, true, "Home", "Example ISP")
	for _, text := range []string{"Home · Example ISP", "↓12", "↑1.2", "max 20"} {
		if !strings.Contains(got, text) {
			t.Errorf("%q missing from %q", text, got)
		}
	}
	if strings.Contains(adapterRow(s, false, "", ""), "flows") {
		t.Fatal("paused row reported relayed flows")
	}
	if s.Name != "Ethernet 5" {
		t.Fatal("row formatting renamed the scheduling identity")
	}
}

// The Windows 11 menu's render test (internal/winmenu/render_full_windows_test.go) draws these exact
// rows. If adapterRow changes, change them there too, so the pictures show what the tray shows.
func TestAdapterRowsAsTheWindowsMenuShowsThem(t *testing.T) {
	lrm := string(rune(0x200E)) // the mark adapterRow puts after the name
	mbit := func(m float64) float64 { return m * 125000 }
	for _, c := range []struct {
		s          engine.SlotStats
		alias, isp string
		want       string
	}{
		{engine.SlotStats{Name: "Ethernet 3", LinkIn: mbit(16), LinkOut: mbit(0.2), State: "up"}, "", "",
			"Ethernet 3" + lrm + "  ↓16 ↑0.2"},
		{engine.SlotStats{Name: "Wi-Fi", LinkIn: mbit(79), LinkOut: mbit(7), State: "up", LimitMbps: 100}, "Home", "Example ISP",
			"Home · Example ISP" + lrm + "  ↓79 ↑7.0  max 100"},
		{engine.SlotStats{Name: "iPhone USB", LinkIn: mbit(31), LinkOut: mbit(3.1), State: "up", CoolMbps: 20}, "", "",
			"iPhone USB" + lrm + "  ↓31 ↑3.1  cooling 20"},
	} {
		if got := adapterRow(c.s, true, c.alias, c.isp); got != c.want {
			t.Errorf("adapterRow(%s) = %q, want %q", c.s.Name, got, c.want)
		}
	}
	// While paused nothing is being held back, so the cooling note is not shown.
	if got := adapterRow(engine.SlotStats{Name: "iPhone USB", State: "up", CoolMbps: 20}, false, "", ""); strings.Contains(got, "cooling") {
		t.Errorf("a paused row says it is cooling: %q", got)
	}
}

func TestVersionLabel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0.3.3", "v0.3.3"},
		{"v0.3.3", "v0.3.3"}, // already prefixed: do not double it
		{"dev", "dev"},
		{"", "dev"}, // a build without -X main.version
	} {
		if got := versionLabel(c.in); got != c.want {
			t.Errorf("versionLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUpdateLabel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"v0.3.4", "v0.3.4 (Update Available)"},
		{"0.3.8-test", "v0.3.8-test (Update Available)"},
	} {
		if got := updateLabel(c.in); got != c.want {
			t.Errorf("updateLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrayTooltipFitsWindowsLimit(t *testing.T) {
	const mbit = 125_000
	mk := func(name string, down, up float64) engine.SlotStats {
		return engine.SlotStats{Name: name, Present: true, State: "up", LinkIn: down * mbit, LinkOut: up * mbit}
	}
	two := []engine.SlotStats{mk("Ethernet 3", 16, 0.2), mk("Ethernet 4", 79, 7)}
	tip := trayTooltip(stateWord(true), 95*mbit, 7.2*mbit, two)
	for _, want := range []string{"Merging", "Eth 3 ↓16 ↑0.2", "Eth 4 ↓79 ↑7.0", "Total ↓95 ↑7.2 Mbps"} {
		if utf16Len(tip) > tooltipMax || !strings.Contains(tip, want) {
			t.Fatalf("two adapters should fit with full detail (%q), got %d chars:\n%s", want, utf16Len(tip), tip)
		}
	}
	four := append(two, mk("Wi-Fi", 55, 3), mk("Ethernet 7", 12, 1))
	tip = trayTooltip(stateWord(false), 162*mbit, 11.2*mbit, four)
	if utf16Len(tip) > tooltipMax {
		t.Fatalf("tooltip is %d chars, Windows shows only %d:\n%s", utf16Len(tip), tooltipMax+1, tip)
	}
	for _, want := range []string{"Paused", "Eth 3 ↓", "Eth 4 ↓", "WiFi ↓", "Eth 7 ↓", "Total ↓162 ↑11"} {
		if !strings.Contains(tip, want) {
			t.Fatalf("every adapter keeps its download speed and the total is always shown; %q missing:\n%s", want, tip)
		}
	}
}

func TestLimitLabel(t *testing.T) {
	for in, want := range map[float64]string{1: "1", 2.5: "2.5", 100: "100"} {
		if got := limitLabel(in); got != want {
			t.Errorf("limitLabel(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestLimitTextUsesMbit(t *testing.T) {
	for in, want := range map[float64]string{1: "1 Mbit/s", 12.5: "12.5 Mbit/s", 1000: "1000 Mbit/s"} {
		if got := limitText(in); got != want {
			t.Errorf("limitText(%v) = %q, want %q", in, got, want)
		}
	}
	if tip := limitTip(20); !strings.Contains(tip, "20 Mbit/s") {
		t.Errorf("limitTip(20) = %q, want it to say 20 Mbit/s", tip)
	}
	if tip := limitTip(0); !strings.Contains(tip, "full speed") {
		t.Errorf("limitTip(0) = %q, want it to describe no cap", tip)
	}
}

func TestCoolTipSaysItIsOptIn(t *testing.T) {
	tip := coolTip(20)
	for _, want := range []string{"Off:", "full speed", "20 Mbit/s", "Remembered"} {
		if !strings.Contains(tip, want) {
			t.Errorf("tooltip %q does not mention %q", tip, want)
		}
	}
}

// Ticking one saved box must not forget the other.
func TestSaveSettingKeepsTheOthers(t *testing.T) {
	u := &trayUI{stateDir: t.TempDir()}
	if err := u.saveSetting(func(s *state.Settings) { s.PhoneCooling = true }); err != nil {
		t.Fatal(err)
	}
	if err := u.saveSetting(func(s *state.Settings) { s.GameMode = true }); err != nil {
		t.Fatal(err)
	}
	got, err := state.LoadSettings(u.stateDir)
	if err != nil || !got.PhoneCooling || !got.GameMode {
		t.Fatalf("saved %+v, %v", got, err)
	}
}

func TestSpeedNow(t *testing.T) {
	live := []engine.SlotStats{
		{Name: "en0", IP: "192.168.0.5", LinkIn: 1e6, LinkOut: 2e5},
		{Name: "en5", IP: "10.0.0.2", IP6: "2001:db8::2", LinkIn: 3e6, LinkOut: 4e5},
	}
	def := func() string { return "2001:db8::2" }
	if down, up, ok := speedNow(true, live, def); !ok || down != 4e6 || up != 6e5 {
		t.Errorf("merging: %v %v %v, want the total", down, up, ok)
	}
	if down, up, ok := speedNow(false, live, def); !ok || down != 3e6 || up != 4e5 {
		t.Errorf("paused: %v %v %v, want the default adapter's", down, up, ok)
	}
	if _, _, ok := speedNow(false, live, func() string { return "" }); ok {
		t.Error("paused with no internet: there is no default adapter to show")
	}
}
