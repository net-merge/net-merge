package tray

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"

	"fyne.io/systray"

	"net-merge/internal/logx"
	"net-merge/internal/nics"
	"net-merge/internal/platform"
	"net-merge/internal/update"
)

func (u *trayUI) setupNative() {
	p, err := newWindowsPanel(u.stateDir, u.nativeClicks)
	if err != nil {
		logx.Printf("native flyout: %v; using the standard menu\n", err)
		return
	}
	u.native = p
	systray.SetOnTapped(p.Show)
	systray.SetOnSecondaryTapped(p.Show)
}

func (u *trayUI) nativeSignIn() {
	on := !u.signInItem.Checked()
	u.lastErr = ""
	if err := platform.SetAutostart(on); err != nil {
		u.lastErr = "start at sign-in: " + err.Error()
	} else if on {
		u.signInItem.Check()
	} else {
		u.signInItem.Uncheck()
	}
}

func (u *trayUI) nativeAdapter(name string, enabled bool) {
	// Only a currently enumerated physical adapter can reach this command.
	// Pass the name as an exec argument; never interpolate it into a shell.
	hardware, err := nics.HardwareAdapters()
	if err != nil {
		u.lastErr = err.Error()
		return
	}
	found := false
	for _, h := range hardware {
		if h.Name == name {
			found = true
			break
		}
	}
	if !found {
		u.lastErr = "physical adapter no longer available"
		return
	}
	if _, err := net.InterfaceByName(name); err != nil {
		u.lastErr = err.Error()
		return
	}
	admin := "disabled"
	if enabled {
		admin = "enabled"
	}
	cmd := exec.Command("netsh", "interface", "set", "interface", "name="+name, "admin="+admin)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		u.lastErr = fmt.Sprintf("adapter: %s (%v)", strings.TrimSpace(string(output)), err)
	} else {
		u.lastErr = ""
	}
}

func (u *trayUI) nativeVersion() {
	if err := platform.OpenURL(update.ReleasesURL); err != nil {
		u.lastErr = err.Error()
	}
}

func (u *trayUI) nativeQuit() { systray.Quit() }
