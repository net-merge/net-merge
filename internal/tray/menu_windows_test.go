//go:build windows && amd64

package tray

import (
	"testing"
	"unsafe"
)

// The system settings are read through SystemParametersInfo. What they say depends on the PC, so this
// only checks that they can be read and that the structure Windows fills in has the size it expects
// (HIGHCONTRASTW is 16 bytes on 64-bit Windows).
func TestAccessibilitySettingsCanBeRead(t *testing.T) {
	if got := unsafe.Sizeof(highContrastInfo{}); got != 16 {
		t.Errorf("HIGHCONTRASTW is %d bytes, want 16", got)
	}
	if err := pSystemParametersInfo.Find(); err != nil {
		t.Fatal(err)
	}
	t.Logf("screen reader on: %v, high contrast on: %v, native menu: %v",
		screenReaderRunning(), highContrastOn(), systemAccessibility.needsNativeMenu())
}

func TestNativeTextDoublesAmpersands(t *testing.T) {
	for in, want := range map[string]string{
		"Quit":                 "Quit",
		"AT&T Fibre":           "AT&&T Fibre",
		"Home && Away":         "Home &&&& Away",
		"Johnson & Johnson Tv": "Johnson && Johnson Tv",
	} {
		if got := nativeText(in); got != want {
			t.Errorf("nativeText(%q) = %q, want %q", in, got, want)
		}
	}
}
