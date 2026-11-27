//go:build windows && amd64

package tray

import (
	"fyne.io/systray"

	"net-merge/internal/nics"
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

// setAdapterIcon tells the menu what kind of adapter a row is; it draws the matching Fluent icon.
func setAdapterIcon(it *menuItem, kind nics.Kind) { it.SetAdapterKind(string(kind)) }

// menuText leaves the row as it is: this menu draws text itself, so "&" is not a mnemonic here, and the
// text's direction is set by the menu. The mark after an alias (see adapterRow) still keeps the
// numbers on the same side.
func menuText(s string) string { return s }
