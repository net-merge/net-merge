package tray

// The Windows 11 style menu draws itself, so a screen reader cannot read it and high contrast does not
// reach it. While either is on, a click on the icon opens the plain native tray menu instead. The two
// menus hold the same items, so nothing is lost by switching, and the choice follows the system as it
// changes (a screen reader started after net-merge, high contrast switched on from the keyboard).

// accessibility is what that choice depends on. The Windows side reads both from the system.
type accessibility struct {
	screenReader func() bool
	highContrast func() bool
}

// needsNativeMenu reports whether the plain native menu must be used instead of the drawn one. A setting
// that cannot be read counts as off.
func (a accessibility) needsNativeMenu() bool {
	return (a.screenReader != nil && a.screenReader()) || (a.highContrast != nil && a.highContrast())
}

// menuSwitcher tells the tray which menu a click opens, and only when that changes.
type menuSwitcher struct {
	native  bool
	started bool
	apply   func(native bool)
}

func (s *menuSwitcher) update(native bool) {
	if s.started && s.native == native {
		return
	}
	s.started, s.native = true, native
	s.apply(native)
}
