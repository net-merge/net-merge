//go:build windows && amd64

package tray

import (
	"fyne.io/systray"

	"net-merge/internal/winmenu"
)

// On Windows the menu is our own, drawn like the Windows 11 desktop context menu. systray still owns
// the icon; it just opens this instead of its native menu.
type menuItem = winmenu.Item

var (
	addMenuItem         = winmenu.AddMenuItem
	addMenuItemCheckbox = winmenu.AddMenuItemCheckbox
	addSeparator        = winmenu.AddSeparator
)

func startMenu() {
	winmenu.Start()
	systray.SetOnTapped(winmenu.Popup)
	systray.SetOnSecondaryTapped(winmenu.Popup)
}
