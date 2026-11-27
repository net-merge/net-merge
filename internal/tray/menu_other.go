//go:build !(windows && amd64)

package tray

import "fyne.io/systray"

// macOS, Linux and other Windows builds keep systray's native menu.
type menuItem = systray.MenuItem

var (
	addMenuItem         = systray.AddMenuItem
	addMenuItemCheckbox = systray.AddMenuItemCheckbox
	addSeparator        = systray.AddSeparator
)

func startMenu() {}
