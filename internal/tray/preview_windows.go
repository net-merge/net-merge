package tray

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"net-merge/internal/engine"
	"net-merge/internal/nics"
	"net-merge/internal/platform"
	"net-merge/internal/state"
)

// PreviewWindows is a developer-only, read-only preview of the native flyout.
// It never creates a TUN or changes adapter/merging/autostart settings.
func PreviewWindows(dir, version string) error {
	clicks := make(chan nativeAction, 32)
	p, err := newWindowsPanel(dir, clicks, true)
	if err != nil {
		return err
	}
	defer p.Close()
	printed := map[string]string{}
	signIn := platform.AutostartEnabled()
	refresh := func() {
		view := nativeView{State: "Paused", Version: version, V6Auto: true, AutoPause: true, SignIn: signIn}
		if st, ok := state.Read(dir); ok {
			view.On = true
			view.State = "Merging"
			for _, n := range st.NICs {
				if n.Present {
					view.NICs = append(view.NICs, n)
					view.Down += n.LinkIn
					view.Up += n.LinkOut
				}
			}
		} else if adapters, err := nics.DiscoverNics(nil, nil); err == nil {
			for _, n := range adapters {
				view.NICs = append(view.NICs, engine.SlotStats{Name: n.Name, IP: n.IP.String(), IP6: n.IP6.String(), State: "checking", Present: true})
			}
		}
		p.Refresh(view)
		for _, n := range view.NICs {
			if net.ParseIP(n.IP) != nil {
				info := p.resolver.Get(n.Name)
				key := fmt.Sprintf("%s/%t", info.Name, info.Logo != nil)
				if info.Name != "" && printed[n.Name] != key {
					fmt.Printf("%s: %s (%s), logo=%t\n", n.Name, info.Name, info.Domain, info.Logo != nil)
					printed[n.Name] = key
				}
			}
		}
	}
	refresh()
	p.Show()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	for {
		select {
		case <-timeout.C:
			return nil
		case <-stop:
			return nil
		case action := <-clicks:
			if action.kind == "quit" {
				return nil
			}
			fmt.Printf("Preview action (not applied): %s %s\n", action.kind, action.name)
			p.Show()
		case <-timer.C:
			refresh()
		}
	}
}
