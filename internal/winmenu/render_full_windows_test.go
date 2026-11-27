package winmenu

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fullMenu is every row the tray puts in the menu once the adapter rows carry their type, an alias and
// an ISP name, a phone is cooling, and the options for ISP names, games and phone cooling are there.
func fullMenu() []*Item {
	row := func(title string, checkable, checked, disabled bool) *Item {
		it := newItem(title, checked)
		it.checkable, it.disabled = checkable, disabled
		return it
	}
	adapter := func(title, kind string) *Item {
		it := newItem(title, false)
		it.kind = kind
		it.kids = []*Item{newItem("Unlimited", false), newItem("10 Mbit/s", false), newItem("50 Mbit/s", false)}
		return it
	}
	sep := func() *Item { return &Item{sep: true} }
	return []*Item{
		row("Merging  ·  Total ↓126 ↑10.3 Mbps", false, false, true),
		sep(),
		adapter("Ethernet 3"+leftToRight+"  ↓16 ↑0.2 Mbps  (12 flows via net-merge)", "ethernet"),
		adapter("Home · Example ISP"+leftToRight+"  ↓79 ↑7.0 Mbps  limit 100 Mbit/s  (31 flows via net-merge)  +IPv6", "wifi"),
		adapter("iPhone USB"+leftToRight+"  ↓31 ↑3.1 Mbps  cooling at 20 Mbit/s  (9 flows via net-merge)", "phone"),
		sep(),
		row("Show ISP names (online lookup)", true, true, false),
		row("Use all adapters (IPv6 only if every adapter has it)", true, true, false),
		row("Keep a connection free for games and calls", true, true, false),
		row("Pause when there is only one connection", true, true, false),
		row("Cool down phone hotspots", true, false, false),
		row("Start at sign-in", true, false, false),
		row("Pause merging", false, false, false),
		row("v0.3.7", false, false, false),
		row("Quit", false, false, false),
	}
}

// Every row of the full menu gets an icon, drawn in the theme's ink, at every scale; the checked options
// show their check mark on the right and the unchecked ones none. Set WINMENU_PREVIEW_DIR to save the
// menus as PNGs to look at, in both themes.
func TestFullMenuRendersWithIconsInBothThemes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	dir := os.Getenv("WINMENU_PREVIEW_DIR")
	for _, dpi := range []int{96, 120, 144, 192} {
		for _, dark := range []bool{true, false} {
			rows := snapshot(fullMenu())
			var widest int
			w, h := layoutRows(rows, dpi, 0, 0, textWidthAt(dpi))
			widest = w
			const hover = 8 // "Keep a connection free for games and calls"
			if rows[hover].title != "Keep a connection free for games and calls" {
				t.Fatalf("row %d is %q", hover, rows[hover].title)
			}
			rows[hover].glow = 1
			frame := newDIB(w, h)
			render(frame, rows, dpi, dark, nil)

			seen := map[string]string{}
			for i, r := range rows {
				if r.sep {
					continue
				}
				isAdapter := r.hasSub
				if r.icon == "" {
					t.Errorf("dpi %d dark %v: %q has no icon", dpi, dark, r.title)
					continue
				}
				if !isAdapter { // adapters of the same kind may share one; the options must not
					if other, dup := seen[r.icon]; dup {
						t.Errorf("dpi %d dark %v: %q and %q share an icon", dpi, dark, r.title, other)
					}
					seen[r.icon] = r.title
				}
				left := px(dipPadX, dpi) + px(dipIconX, dpi)
				if inkIn(frame.canvas, rect{left, r.y, left + px(dipIconBox, dpi), r.y + r.h}) == 0 {
					t.Errorf("dpi %d dark %v row %d (%q): its icon was not drawn", dpi, dark, i, r.title)
				}
				trailL := w - px(dipPadX, dpi) - px(dipChevronR, dpi) - px(dipChevron, dpi)
				trail := rect{trailL, r.y, trailL + px(dipChevron, dpi), r.y + r.h}
				shown := inkIn(frame.canvas, trail) != 0
				if want := r.hasSub || (r.checkable && r.checked); shown != want {
					t.Errorf("dpi %d dark %v row %d (%q): trailing mark shown=%v, want %v", dpi, dark, i, r.title, shown, want)
				}
			}
			if dpi == 96 && widest > 700 {
				t.Errorf("the full menu is %d px wide at 100%%", widest)
			}
			t.Logf("dpi %d dark %v: %dx%d", dpi, dark, w, h)
			if dir != "" {
				savePreview(t, frame.canvas, filepath.Join(dir, fmt.Sprintf("full-menu-%d-%s.png", dpi, map[bool]string{true: "dark", false: "light"}[dark])), dark)
			}
			frame.free()
		}
	}
}
