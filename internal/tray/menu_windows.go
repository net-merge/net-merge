//go:build windows && amd64

package tray

import (
	"strings"
	"time"
	"unsafe"

	"fyne.io/systray"

	"net-merge/internal/nics"
	"net-merge/internal/winmenu"
)

// On Windows there are two menus with the same items: our own, drawn like the Windows 11 desktop context
// menu, and systray's plain native one. A click on the icon opens ours, unless a screen reader or high
// contrast is on (see menu_mode.go), when it opens the native one. systray still owns the icon.
//
// menuItem is one row in both. Whatever the tray does to it is done to both, and a click on either one
// arrives on ClickedCh.
type menuItem struct {
	ClickedCh chan struct{}
	w         *winmenu.Item
	n         *systray.MenuItem
}

func newMenuItem(w *winmenu.Item, n *systray.MenuItem) *menuItem {
	it := &menuItem{ClickedCh: make(chan struct{}, 1), w: w, n: n}
	go func() {
		for {
			select {
			case <-w.ClickedCh:
			case <-n.ClickedCh:
			}
			select { // a click is dropped if one is already waiting, as in both menus
			case it.ClickedCh <- struct{}{}:
			default:
			}
		}
	}()
	return it
}

// nativeText is a title as the native menu wants it: Windows reads "&" there as a keyboard mnemonic, so
// an ISP name or an alias with one needs it doubled. The drawn menu shows text as it is.
func nativeText(s string) string { return strings.ReplaceAll(s, "&", "&&") }

func addMenuItem(title, tooltip string) *menuItem {
	return newMenuItem(winmenu.AddMenuItem(title, tooltip), systray.AddMenuItem(nativeText(title), tooltip))
}

func addMenuItemCheckbox(title, tooltip string, checked bool) *menuItem {
	return newMenuItem(winmenu.AddMenuItemCheckbox(title, tooltip, checked), systray.AddMenuItemCheckbox(nativeText(title), tooltip, checked))
}

func addSeparator() {
	winmenu.AddSeparator()
	systray.AddSeparator()
}

func (it *menuItem) AddSubMenuItemCheckbox(title, tooltip string, checked bool) *menuItem {
	return newMenuItem(it.w.AddSubMenuItemCheckbox(title, tooltip, checked), it.n.AddSubMenuItemCheckbox(nativeText(title), tooltip, checked))
}

func (it *menuItem) SetTitle(title string) {
	it.w.SetTitle(title)
	it.n.SetTitle(nativeText(title))
}

func (it *menuItem) SetTooltip(tooltip string) { it.n.SetTooltip(tooltip) } // the drawn menu has none
func (it *menuItem) Disable()                  { it.w.Disable(); it.n.Disable() }
func (it *menuItem) Hide()                     { it.w.Hide(); it.n.Hide() }
func (it *menuItem) Show()                     { it.w.Show(); it.n.Show() }
func (it *menuItem) Check()                    { it.w.Check(); it.n.Check() }
func (it *menuItem) Uncheck()                  { it.w.Uncheck(); it.n.Uncheck() }
func (it *menuItem) Checked() bool             { return it.w.Checked() }

func startMenu() {
	winmenu.Start()
	sw := &menuSwitcher{apply: func(native bool) {
		if native { // with no handler, systray opens its own menu
			systray.SetOnTapped(nil)
			systray.SetOnSecondaryTapped(nil)
			return
		}
		systray.SetOnTapped(winmenu.Popup)
		systray.SetOnSecondaryTapped(winmenu.Popup)
	}}
	sw.update(systemAccessibility.needsNativeMenu())
	go func() {
		for range time.Tick(time.Second) {
			sw.update(systemAccessibility.needsNativeMenu())
		}
	}()
}

// setAdapterIcon tells the drawn menu what kind of adapter a row is, and it draws the matching Fluent
// icon. The native menu stays plain.
func setAdapterIcon(it *menuItem, kind nics.Kind) { it.w.SetAdapterKind(string(kind)) }

// menuText leaves the row as it is: each menu makes it right for itself (see nativeText), and the mark
// after an alias (see adapterRow) keeps the numbers on the same side.
func menuText(s string) string { return s }

// setSymbol does nothing here: the drawn menu picks each row's icon from its text (internal/winmenu).
func setSymbol(*menuItem, string) {}

// systemAccessibility reads the two settings from Windows.
var systemAccessibility = accessibility{screenReader: screenReaderRunning, highContrast: highContrastOn}

var pSystemParametersInfo = user32.NewProc("SystemParametersInfoW")

const (
	spiGetHighContrast = 0x0042
	spiGetScreenReader = 0x0046
	hcfHighContrastOn  = 0x00000001
)

// highContrastInfo is HIGHCONTRASTW.
type highContrastInfo struct {
	size, flags   uint32
	defaultScheme *uint16
}

func screenReaderRunning() bool {
	var on int32
	r, _, _ := pSystemParametersInfo.Call(spiGetScreenReader, 0, uintptr(unsafe.Pointer(&on)), 0)
	return r != 0 && on != 0
}

func highContrastOn() bool {
	hc := highContrastInfo{}
	hc.size = uint32(unsafe.Sizeof(hc))
	r, _, _ := pSystemParametersInfo.Call(spiGetHighContrast, uintptr(hc.size), uintptr(unsafe.Pointer(&hc)), 0)
	return r != 0 && hc.flags&hcfHighContrastOn != 0
}
