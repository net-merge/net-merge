//go:build !windows

package tray

func (u *trayUI) setupNative()               {}
func (u *trayUI) nativeSignIn()              {}
func (u *trayUI) nativeAdapter(string, bool) {}
func (u *trayUI) nativeVersion()             {}
func (u *trayUI) nativeQuit()                {}
