// Package winmenu is the Windows tray menu: a popup drawn like the Windows 11 desktop context menu,
// with an acrylic backdrop, instead of the classic menu that TrackPopupMenu draws. Its items have the
// same methods as fyne.io/systray's, so the tray code is the same on every OS.
package winmenu

import "sync"

var (
	mu   sync.Mutex // guards root and every Item's fields
	root []*Item

	changed = func() {} // tells the menu thread that an open menu must be redrawn
)

// Item is one row of the menu. Its methods can be called from any goroutine.
type Item struct {
	// ClickedCh receives when the row is clicked. A click is dropped if one is already waiting.
	ClickedCh chan struct{}

	title                          string
	sep, checked, disabled, hidden bool
	checkable                      bool
	kids                           []*Item
}

func newItem(title string, checked bool) *Item {
	return &Item{ClickedCh: make(chan struct{}, 1), title: title, checked: checked}
}

func addRoot(it *Item) *Item {
	mu.Lock()
	root = append(root, it)
	mu.Unlock()
	changed()
	return it
}

// AddMenuItem adds a row. The menu has no per-row tooltips, like the native Windows menu it replaces.
func AddMenuItem(title, _ string) *Item { return addRoot(newItem(title, false)) }

// AddMenuItemCheckbox adds a row that can show a check mark.
func AddMenuItemCheckbox(title, _ string, checked bool) *Item {
	it := newItem(title, checked)
	it.checkable = true
	return addRoot(it)
}

// AddSeparator adds a dividing line.
func AddSeparator() { addRoot(&Item{sep: true}) }

// AddSubMenuItemCheckbox adds a row to this row's submenu.
func (it *Item) AddSubMenuItemCheckbox(title, _ string, checked bool) *Item {
	kid := newItem(title, checked)
	kid.checkable = true
	mu.Lock()
	it.kids = append(it.kids, kid)
	mu.Unlock()
	changed()
	return kid
}

// AddSubMenuItem adds a plain row (no check mark) to this row's submenu.
func (it *Item) AddSubMenuItem(title, _ string) *Item {
	kid := newItem(title, false)
	mu.Lock()
	it.kids = append(it.kids, kid)
	mu.Unlock()
	changed()
	return kid
}

func (it *Item) set(f func()) {
	mu.Lock()
	f()
	mu.Unlock()
	changed()
}

func (it *Item) SetTitle(title string) { it.set(func() { it.title = title }) }
func (it *Item) SetTooltip(string)     {}
func (it *Item) Disable()              { it.set(func() { it.disabled = true }) }
func (it *Item) Enable()               { it.set(func() { it.disabled = false }) }
func (it *Item) Hide()                 { it.set(func() { it.hidden = true }) }
func (it *Item) Show()                 { it.set(func() { it.hidden = false }) }
func (it *Item) Check()                { it.set(func() { it.checked = true }) }
func (it *Item) Uncheck()              { it.set(func() { it.checked = false }) }

// Checked reports whether the row shows a check mark.
func (it *Item) Checked() bool {
	mu.Lock()
	defer mu.Unlock()
	return it.checked
}

func (it *Item) click() {
	select {
	case it.ClickedCh <- struct{}{}:
	default:
	}
}

// snapshot is what a menu shows right now. Hidden rows are left out, and so are separators that
// would then have nothing to divide.
func snapshot(items []*Item) []row {
	mu.Lock()
	defer mu.Unlock()
	var rows []row
	for _, it := range items {
		switch {
		case it.hidden:
		case it.sep:
			if len(rows) > 0 && !rows[len(rows)-1].sep {
				rows = append(rows, row{sep: true})
			}
		default:
			hasSub := len(it.kids) > 0
			rows = append(rows, row{item: it, title: it.title, icon: iconFor(it.title, hasSub), checked: it.checked,
				checkable: it.checkable, disabled: it.disabled, hasSub: hasSub})
		}
	}
	if n := len(rows); n > 0 && rows[n-1].sep {
		rows = rows[:n-1]
	}
	return rows
}
