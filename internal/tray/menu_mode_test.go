package tray

import "testing"

func TestNativeMenuIsUsedForScreenReadersAndHighContrast(t *testing.T) {
	yes, no := func() bool { return true }, func() bool { return false }
	for _, c := range []struct {
		name             string
		screenReader, hc func() bool
		wantNative       bool
	}{
		{"neither", no, no, false},
		{"screen reader only", yes, no, true},
		{"high contrast only", no, yes, true},
		{"both", yes, yes, true},
		{"nothing could be read", nil, nil, false},
		{"only the screen reader could be read", yes, nil, true},
		{"only high contrast could be read", nil, yes, true},
	} {
		got := accessibility{screenReader: c.screenReader, highContrast: c.hc}.needsNativeMenu()
		if got != c.wantNative {
			t.Errorf("%s: native menu = %v, want %v", c.name, got, c.wantNative)
		}
	}
}

// The decision is read again and again; the tray is only told when it changes, so the click handlers are
// not rewritten every second.
func TestMenuSwitcherActsOnlyOnChange(t *testing.T) {
	var calls []bool
	s := &menuSwitcher{apply: func(native bool) { calls = append(calls, native) }}
	for _, native := range []bool{false, false, false, true, true, false, false, true} {
		s.update(native)
	}
	want := []bool{false, true, false, true} // the first reading always sets it up
	if len(calls) != len(want) {
		t.Fatalf("apply was called with %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("apply was called with %v, want %v", calls, want)
		}
	}
	s2 := &menuSwitcher{apply: func(native bool) { calls = append(calls, native) }}
	calls = nil
	s2.update(true) // a first reading of "native" must be applied too, not mistaken for the zero value
	if len(calls) != 1 || !calls[0] {
		t.Errorf("a first native reading gave %v", calls)
	}
}
