//go:build traylive && windows && amd64

// The live test makes a real tray icon for a moment. It needs a desktop session, so it only runs when
// asked: go test -tags traylive -run TestBothMenusLive ./internal/tray
package tray

import (
	"strings"
	"testing"
	"time"

	"fyne.io/systray"
)

func TestBothMenusLive(t *testing.T) {
	ready, done := make(chan struct{}), make(chan struct{})
	var (
		row, box, sub, plain *menuItem
	)
	go systray.Run(func() {
		startMenu()
		plain = addMenuItem("AT&T Fibre", "")
		addSeparator()
		box = addMenuItemCheckbox("Game Mode", "", false)
		row = addMenuItem("Ethernet", "")
		sub = row.AddSubMenuItemCheckbox("10 Mbit/s", "", false)
		row.Hide()
		row.Show()
		close(ready)
	}, func() { close(done) })

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the tray did not start")
	}
	defer func() {
		systray.Quit()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the tray did not quit")
		}
	}()

	// A click in either menu arrives on the one channel the tray listens to, including in a submenu.
	for name, send := range map[string]func(*menuItem){
		"drawn menu":  func(it *menuItem) { it.w.ClickedCh <- struct{}{} },
		"native menu": func(it *menuItem) { it.n.ClickedCh <- struct{}{} },
	} {
		for _, it := range []*menuItem{plain, box, sub} {
			go send(it)
			select {
			case <-it.ClickedCh:
			case <-time.After(2 * time.Second):
				t.Errorf("a click in the %s did not arrive", name)
			}
		}
	}

	// A tick shows in both menus, and goes again.
	box.Check()
	if !box.w.Checked() || !box.n.Checked() {
		t.Errorf("after Check: drawn %v, native %v", box.w.Checked(), box.n.Checked())
	}
	box.Uncheck()
	if box.w.Checked() || box.n.Checked() {
		t.Errorf("after Uncheck: drawn %v, native %v", box.w.Checked(), box.n.Checked())
	}

	// The native menu gets "&" doubled, so it is not read as a mnemonic; the drawn menu is not asked to.
	if !strings.Contains(plain.n.String(), "AT&&T Fibre") {
		t.Errorf("the native title is %s", plain.n)
	}
	plain.SetTitle("Home & Away")
	if !strings.Contains(plain.n.String(), "Home && Away") {
		t.Errorf("the native title after SetTitle is %s", plain.n)
	}
}
