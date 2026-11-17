package tray

import (
	"testing"

	"net-merge/internal/engine"
	"net-merge/internal/nics"
	"net-merge/internal/provider"
	"net-merge/internal/state"
)

func TestFlyoutShowsProviderAndAllowsReenablingDisabledHardware(t *testing.T) {
	p := &windowsPanel{prefs: state.TrayPreferences{Aliases: map[string]string{}}}
	w := &flyoutWindow{panel: p, dpi: 96}
	m := panelSnapshot{nativeView: nativeView{NICs: []engine.SlotStats{{Name: "Ethernet", State: "up", Present: true}}}, providers: map[string]provider.Info{"Ethernet": {Name: "Example ISP"}}, hardware: []nics.Hardware{{Name: "Wi-Fi", Enabled: false, Wireless: true}}}
	rows := w.mainRows(m)
	seen := map[string]bool{}
	for _, r := range rows {
		if r.kind == "adaptermenu" {
			seen[r.name] = true
			if r.name == "Ethernet" && r.label != "Example ISP" {
				t.Fatal("provider name missing")
			}
		}
	}
	if !seen["Ethernet"] || !seen["Wi-Fi"] {
		t.Fatal("disabled adapter disappeared and cannot be reenabled")
	}
	w.adapter = "Wi-Fi"
	found := false
	for _, r := range w.subRows(m) {
		if r.kind == "adapter" {
			found = true
			if r.value != 1 || r.label != "Enable adapter" {
				t.Fatal("disabled hardware has no enable action")
			}
		}
	}
	if !found {
		t.Fatal("adapter action missing")
	}
}

func TestFlyoutPreservesCustomCapsAndUnderlyingAdapterNames(t *testing.T) {
	p := &windowsPanel{prefs: state.TrayPreferences{Aliases: map[string]string{"Ethernet": "My provider"}}}
	w := &flyoutWindow{panel: p, dpi: 96, adapter: "Ethernet"}
	m := panelSnapshot{nativeView: nativeView{NICs: []engine.SlotStats{{Name: "Ethernet", LimitMbps: 12.5}}}}
	custom, large := false, false
	for _, r := range w.subRows(m) {
		if r.kind != "limit" {
			continue
		}
		if r.name != "Ethernet" {
			t.Fatal("display alias changed the adapter used by saved limits")
		}
		if r.value == 12.5 && r.checked {
			custom = true
		}
		if r.value == 1000 {
			large = true
		}
	}
	if !custom || !large {
		t.Fatal("custom cap or 1000 Mbit/s option missing")
	}
}

func TestSystemAccentRemainsReadableInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		background := rgb(249, 249, 249)
		if dark {
			background = rgb(32, 32, 32)
		}
		for _, accent := range []uint32{rgb(90, 0, 100), rgb(255, 240, 0), rgb(0, 100, 200)} {
			fg := readableAccent(accent, background, dark)
			a, b := luminance(fg), luminance(background)
			if (max(a, b)+0.05)/(min(a, b)+0.05) < 4.5 {
				t.Fatal("system accent became unreadable")
			}
		}
	}
}
