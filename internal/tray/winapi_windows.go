package tray

import "golang.org/x/sys/windows"

// The Windows libraries the tray calls directly. System32 only, so a planted copy is never loaded.
var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
)
