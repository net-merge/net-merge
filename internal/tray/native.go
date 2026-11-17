package tray

import "net-merge/internal/engine"

type nativeAction struct {
	kind  string
	name  string
	value float64
}

type nativeView struct {
	On                        bool
	State                     string
	Down, Up                  float64
	NICs                      []engine.SlotStats
	V6Auto, AutoPause, SignIn bool
	Version, Error            string
}

type nativePanel interface {
	Show()
	Refresh(nativeView)
	Close()
	SetLatest(string)
}

func (u *trayUI) nativeAction(a nativeAction) {
	switch a.kind {
	case "merge":
		u.manual(!u.tun.IsOn())
	case "ipv6":
		if u.bal.V6Mode() == engine.V6Auto {
			u.bal.SetV6Mode(engine.V6On)
			u.v6Item.Uncheck()
		} else {
			u.bal.SetV6Mode(engine.V6Auto)
			u.v6Item.Check()
		}
	case "auto":
		if u.ap.enabled {
			u.autoItem.Uncheck()
		} else {
			u.autoItem.Check()
		}
		u.ap.setEnabled(!u.ap.enabled, u.tun.IsOn())
	case "limit":
		u.applyLimit(a.name, a.value)
	case "signin":
		u.nativeSignIn()
	case "adapter":
		u.nativeAdapter(a.name, a.value != 0)
	case "version":
		u.nativeVersion()
	case "quit":
		u.nativeQuit()
	}
}
