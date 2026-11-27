//go:build !(windows && amd64)

package tray

import (
	"runtime"
	"strings"

	"fyne.io/systray"

	"net-merge/internal/icon"
	"net-merge/internal/nics"
)

// macOS, Linux and other Windows builds keep systray's native menu.
type menuItem = systray.MenuItem

var (
	addMenuItem         = systray.AddMenuItem
	addMenuItemCheckbox = systray.AddMenuItemCheckbox
	addSeparator        = systray.AddSeparator
)

func startMenu() {}

// setAdapterIcon shows the adapter's type (Wi-Fi, Ethernet, phone, other) beside its row.
func setAdapterIcon(it *menuItem, kind nics.Kind) {
	template, regular := icon.Adapter(string(kind))
	it.SetTemplateIcon(template, regular)
}

// setSymbol shows a menu row's icon.
func setSymbol(it *menuItem, name string) {
	template, regular := icon.Adapter(name)
	it.SetTemplateIcon(template, regular)
}

// leftToRight is U+200E, an invisible mark. In front of a row it keeps the row's numbers on the same
// side after an Arabic or Hebrew name.
const leftToRight = string(rune(0x200E))

// menuText makes an adapter row safe for the native menu. Windows reads "&" there as a keyboard
// mnemonic, so ISP names and aliases that contain one need it doubled.
func menuText(s string) string {
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "&", "&&")
	}
	return leftToRight + s
}
