//go:build menulive

// The live test opens the real menu on this PC's screen, moves the pointer with SetCursorPos and samples
// the screen. It needs a desktop session, so it only runs when asked: go test -tags menulive ./internal/winmenu
package winmenu

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

var pSetCursorPos = user32.NewProc("SetCursorPos")

func grab(r rect) *dib {
	shot := newDIB(r.right-r.left, r.bottom-r.top)
	screen, _, _ := pGetDC.Call(0)
	pBitBlt.Call(shot.dc, 0, 0, uintptr(shot.w), uintptr(shot.h), screen, uintptr(r.left), uintptr(r.top), 0x00CC0020)
	pReleaseDC.Call(0, screen)
	pGdiFlush.Call()
	return shot
}

// diff is the mean difference between two captures of the same place, inside r (in their pixels).
func diff(a, b *dib, r rect) float64 {
	s, n := 0.0, 0.0
	for y := max(r.top, 0); y < min(r.bottom, a.h); y++ {
		for x := max(r.left, 0); x < min(r.right, a.w); x++ {
			i := (y*a.w + x) * 4
			for c := 0; c < 3; c++ {
				s += math.Abs(float64(a.pix[i+c]) - float64(b.pix[i+c]))
			}
			n += 3
		}
	}
	return s / n
}

func moveTo(x, y int) { pSetCursorPos.Call(uintptr(x), uintptr(y)) }

// series samples a place on screen every ~12 ms for d, as each sample's difference from before.
func series(area, inside rect, before *dib, d time.Duration) []float64 {
	var out []float64
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(12 * time.Millisecond) {
		s := grab(area)
		out = append(out, math.Round(diff(before, s, inside)*10)/10)
		s.free()
	}
	return out
}

// intermediate counts the samples strictly between the first and the settled value (10%..90%).
func intermediate(v []float64) int {
	lo, hi := v[0], v[len(v)-1]
	n := 0
	for _, x := range v {
		if f := (x - lo) / (hi - lo); f > 0.1 && f < 0.9 {
			n++
		}
	}
	return n
}

func TestMenuLive(t *testing.T) {
	// The menu's thread works in real pixels; so must this one, or the coordinates are off.
	runtime.LockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	dir := os.Getenv("WINMENU_PREVIEW_DIR")
	var saved point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&saved)))
	defer moveTo(int(saved.x), int(saved.y))

	Start()
	AddMenuItem("Merging  ·  Total ↓95 ↑7.2 Mbps", "").Disable()
	AddSeparator()
	a1 := AddMenuItem("Ethernet 3:  ↓16 ↑0.2 Mbps  (12 flows via net-merge)", "")
	a2 := AddMenuItem("Wi-Fi:  ↓79 ↑7.0 Mbps  (31 flows via net-merge)", "")
	for _, a := range []*Item{a1, a2} {
		for _, s := range []string{"Unlimited", "50 Mbit/s", "100 Mbit/s"} {
			a.AddSubMenuItemCheckbox(s, "", false)
		}
	}
	AddSeparator()
	AddMenuItem("Quit", "")

	// The work area of the primary monitor, and the pointer near its bottom-right corner.
	mon, _, _ := pMonitorFromPoint.Call(0, 1)
	mi := monitorInfo{}
	mi.size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	work := rect{int(mi.work.left), int(mi.work.top), int(mi.work.right), int(mi.work.bottom)}
	moveTo(work.right-60, work.bottom-30)
	time.Sleep(100 * time.Millisecond)
	area := rect{work.right - 900, work.bottom - 600, work.right, work.bottom}
	before := grab(area)

	Popup()
	fadeIn := series(area, rect{0, 0, area.right - area.left, area.bottom - area.top}, before, 700*time.Millisecond)
	t.Logf("fade-in samples: %v", fadeIn)
	if intermediate(fadeIn) < 2 {
		t.Errorf("the menu didn't fade in: %v", fadeIn)
	}
	if len(levels) != 1 {
		t.Fatalf("menu not open")
	}
	l := levels[0]
	gap := px(dipEdgeGap, menuDPI)
	t.Logf("menu at %d,%d %dx%d; work area right %d bottom %d", l.x, l.y, l.w, l.h, work.right, work.bottom)
	if work.right-(l.x+l.w) != gap || work.bottom-(l.y+l.h) != gap {
		t.Errorf("the menu should sit %d px from the taskbar and the screen's edge", gap)
	}
	if dir != "" {
		s := grab(area)
		savePreview(t, s.canvas, filepath.Join(dir, "live-menu.png"), menuDark)
		s.free()
	}

	rowY := func(it *Item) int {
		r := l.rows[rowOf(l, it)]
		return l.y + r.y + r.h/2
	}
	wait := func() { time.Sleep(380 * time.Millisecond) }
	open := func() *Item {
		if len(levels) < 2 {
			return nil
		}
		return levels[0].subFor
	}
	step := func(what string, want *Item) {
		t.Helper()
		if got := open(); got != want {
			t.Errorf("%s: wrong submenu open (levels %d, hot %d)", what, len(levels), levels[0].hot)
		}
	}

	moveTo(l.x+l.w/2, rowY(a1))
	wait()
	step("pointer resting on Ethernet 3", a1)

	// Diagonally from Ethernet 3's row into its submenu, across Wi-Fi's row: the submenu stays.
	sub := levels[1]
	moveTo(sub.x-30, rowY(a2))
	time.Sleep(40 * time.Millisecond)
	moveTo(sub.x+sub.w/2, sub.y+sub.h/2)
	wait()
	step("across Wi-Fi's row into Ethernet 3's submenu", a1)
	if hot := levels[0].hot; hot != rowOf(levels[0], a1) {
		t.Errorf("Ethernet 3's row should stay lit, hot is %d", hot)
	}

	moveTo(l.x+l.w/2, rowY(a2))
	wait()
	step("pointer resting on Wi-Fi", a2)

	before.free()
	before = grab(area)
	Popup() // a second click on the icon closes it
	fadeOut := series(area, rect{0, 0, area.right - area.left, area.bottom - area.top}, before, 250*time.Millisecond)
	t.Logf("fade-out samples: %v", fadeOut)
	if intermediate(fadeOut) < 1 {
		t.Errorf("the menu didn't fade out: %v", fadeOut)
	}
	before.free()
	if len(closing) != 0 {
		t.Errorf("%d windows still fading after 250 ms", len(closing))
	}
}
