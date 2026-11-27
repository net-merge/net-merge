//go:build !windows

package tray

import "fyne.io/systray"

// setTraySpeed puts the speed beside the menu bar icon; an empty down hides it.
func setTraySpeed(down, up string, _ bool) {
	if down == "" {
		systray.SetTitle("")
		return
	}
	systray.SetTitle(" ↓" + down + " ↑" + up)
}
