package winmenu

import (
	"bytes"
	"fmt"
	"image"
	imgcolor "image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func previewRows() []row {
	rows := []row{
		{item: &Item{}, title: "Merging  ·  Total ↓95 ↑7.2 Mbps", disabled: true},
		{sep: true},
		{item: &Item{}, title: "Ethernet 3:  ↓16 ↑0.2 Mbps  (12 flows via net-merge)", hasSub: true},
		{item: &Item{}, title: "Wi-Fi:  ↓79 ↑7.0 Mbps  (31 flows via net-merge)  +IPv6", hasSub: true},
		{sep: true},
		{item: &Item{}, title: "Use all adapters (IPv6 only if every adapter has it)", checked: true, checkable: true},
		{item: &Item{}, title: "Pause when there is only one connection", checked: true, checkable: true},
		{item: &Item{}, title: "Start at sign-in", checkable: true},
		{item: &Item{}, title: "Pause merging"},
		{item: &Item{}, title: "v0.3.3"},
		{item: &Item{}, title: "Quit"},
	}
	for i := range rows {
		rows[i].icon = iconFor(rows[i].title, rows[i].hasSub)
	}
	return rows
}

// The icons must be readable in both themes: light strokes in dark mode, dark strokes in light mode.
func TestIconsFollowTheTheme(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	for _, dpi := range []int{96, 192} {
		for _, dark := range []bool{true, false} {
			rows := previewRows()
			w, h := layoutRows(rows, dpi, 0, 0, textWidthAt(dpi))
			frame := newDIB(w, h)
			render(frame, rows, dpi, dark, nil)
			quit := rows[len(rows)-1]
			left := px(dipPadX, dpi) + px(dipIconX, dpi)
			strokes, wrong := 0, 0
			for y := quit.y; y < quit.y+quit.h; y++ {
				for x := left; x < left+px(dipIconBox, dpi); x++ {
					p := frame.pix[(y*w+x)*4 : (y*w+x)*4+4]
					if p[3] < 200 { // not a solid part of a stroke
						continue
					}
					if dark && p[1] > 200 || !dark && p[1] < 60 {
						strokes++
					} else if dark && p[1] < 30 || !dark && p[1] > 225 {
						wrong++
					}
				}
			}
			if strokes == 0 {
				t.Errorf("dpi %d dark %v: the Quit icon has no strokes in the theme's colour", dpi, dark)
			}
			if wrong != 0 {
				t.Errorf("dpi %d dark %v: %d icon pixels are in the wrong colour for the theme", dpi, dark, wrong)
			}
			frame.free()
		}
	}
}

// inkIn counts the pixels in r that differ from the empty background, taken from the menu's top inset.
func inkIn(cv canvas, r rect) (n int) {
	blank := cv.pix[(cv.w/2)*4 : (cv.w/2)*4+4]
	for y := r.top; y < r.bottom; y++ {
		for x := r.left; x < r.right; x++ {
			if !bytes.Equal(cv.pix[(y*cv.w+x)*4:(y*cv.w+x)*4+4], blank) {
				n++
			}
		}
	}
	return n
}

// Draws with the real fonts at each scale. Set WINMENU_PREVIEW_DIR to also save PNGs to look at.
func TestRenderAtEachScale(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	dir := os.Getenv("WINMENU_PREVIEW_DIR")
	var width100 int
	for _, dpi := range []int{96, 120, 144, 192} {
		for _, dark := range []bool{true, false} {
			rows := previewRows()
			w, h := layoutRows(rows, dpi, 0, 0, textWidthAt(dpi))
			frame := newDIB(w, h)
			const hot = 3
			rows[hot].glow = 1
			render(frame, rows, dpi, dark, nil)

			quit := rows[len(rows)-1]
			x := px(dipPadX, dpi) + px(dipTextX, dpi)
			if inkIn(frame.canvas, rect{x, quit.y, x + px(40, dpi), quit.y + quit.h}) == 0 {
				t.Errorf("dpi %d: the Quit text was not drawn", dpi)
			}
			check := rows[5]
			if inkIn(frame.canvas, rect{px(dipPadX, dpi), check.y, x, check.y + check.h}) == 0 {
				t.Errorf("dpi %d: the icon was not drawn", dpi)
			}
			trailL := w - px(dipPadX, dpi) - px(dipChevronR, dpi) - px(dipChevron, dpi)
			trail := rect{trailL, check.y, trailL + px(dipChevron, dpi), check.y + check.h}
			if inkIn(frame.canvas, trail) == 0 {
				t.Errorf("dpi %d: the check mark was not drawn beside the icon's row", dpi)
			}
			unchecked := rows[7]
			trail.top, trail.bottom = unchecked.y, unchecked.y+unchecked.h
			if inkIn(frame.canvas, trail) != 0 {
				t.Errorf("dpi %d: an unchecked row shows a check mark", dpi)
			}
			if a := frame.pix[((rows[hot].y+rows[hot].h/2)*w+px(dipPadX, dpi)+px(4, dpi))*4+3]; a == 0 {
				t.Errorf("dpi %d: no hover highlight", dpi)
			}
			// The acrylic must keep showing through: the inset has only the base layer, never opaque.
			want := uint8(0)
			if hasSystemBackdrop() {
				want = uint8(math.Round(255 * paletteFor(dark).base.a))
			}
			if a := frame.pix[(px(dipPadY, dpi)/2*w+w/2)*4+3]; a != want {
				t.Errorf("dpi %d dark %v: top inset alpha %d, want %d", dpi, dark, a, want)
			}
			if dpi == 96 {
				width100 = w
			}
			if dpi == 192 {
				// Text is hinted per size, so allow a few percent; it must still be about twice as wide.
				if w < width100*19/10 || w > width100*21/10 {
					t.Errorf("width is %d at 100%% but %d at 200%%", width100, w)
				}
			}
			t.Logf("dpi %d: %dx%d", dpi, w, h)
			if dir != "" {
				savePreview(t, frame.canvas, filepath.Join(dir, fmt.Sprintf("menu-%d-dark-%v.png", dpi, dark)), dark)
			}
			frame.free()
		}
	}
}

// The highlight fades: part way through, a row is lit part of the way, between unlit and fully lit.
func TestHighlightFollowsItsGlow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	const dpi = 96
	rows := previewRows()
	w, h := layoutRows(rows, dpi, 0, 0, textWidthAt(dpi))
	frame := newDIB(w, h)
	defer frame.free()
	const hot = 3
	at := (rows[hot].y+rows[hot].h/2)*w + px(dipPadX, dpi) + px(4, dpi)
	shade := func(glow float64) int {
		rows[hot].glow = glow
		render(frame, rows, dpi, false, nil)
		return int(frame.pix[at*4+1])
	}
	unlit, half, lit := shade(0), shade(0.5), shade(1)
	if !(lit < half && half < unlit) {
		t.Errorf("glow 0, 0.5 and 1 gave %d, %d and %d: each should be darker than the last", unlit, half, lit)
	}
}

// Windows can activate the menu after it has been painted. Left to itself, that draws a white window
// frame over the menu's edge, so the menu's own pixels must survive it.
func TestActivationKeepsThePixels(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiContext.Call(dpiPMv2)
	Start()
	const w, h = 200, 100
	l := &level{hot: -1, w: w, h: h}
	createWindow(l, wsExNoActivate)
	if l.hwnd == 0 {
		t.Skip("no window could be created")
	}
	defer destroy(l)
	setCloaked(l.hwnd, true)
	pShowWindow.Call(l.hwnd, swShowNA)

	const srcCopy = 0x00CC0020
	getDC, releaseDC := user32.NewProc("GetDC"), user32.NewProc("ReleaseDC")
	send := user32.NewProc("SendMessageW")
	dc, _, _ := getDC.Call(l.hwnd)
	defer releaseDC.Call(l.hwnd, dc)

	mark := color{10, 20, 30, 1}
	l.frame.fill(mark)
	pBitBlt.Call(dc, 0, 0, w, h, l.frame.dc, 0, 0, srcCopy)

	send.Call(l.hwnd, wmNCActivate, 1, 0)
	send.Call(l.hwnd, wmNCPaint, 1, 0)

	shot := newDIB(w, h)
	defer shot.free()
	pBitBlt.Call(shot.dc, 0, 0, w, h, dc, 0, 0, srcCopy)
	pGdiFlush.Call()
	for _, at := range [][2]int{{0, 0}, {0, h / 2}, {w - 1, h / 2}, {w / 2, 0}, {w / 2, h - 1}, {w / 2, h / 2}} {
		p := shot.pix[(at[1]*w+at[0])*4:]
		if p[0] != mark.b || p[1] != mark.g || p[2] != mark.r {
			t.Errorf("pixel %v is %d,%d,%d after activation, want the menu's %d,%d,%d", at, p[2], p[1], p[0], mark.r, mark.g, mark.b)
		}
	}
}

func savePreview(t *testing.T, cv canvas, path string, dark bool) {
	bg := uint8(0xF3)
	if dark {
		bg = 0x2C
	}
	img := image.NewRGBA(image.Rect(0, 0, cv.w, cv.h))
	for y := 0; y < cv.h; y++ {
		for x := 0; x < cv.w; x++ {
			p := cv.pix[(y*cv.w+x)*4:]
			inv := 255 - uint32(p[3])
			// Clamped: the light base's channels exceed its alpha (see lightLevel), so this can pass 255.
			over := func(c byte) uint8 { return uint8(min(uint32(c)+uint32(bg)*inv/255, 255)) }
			img.SetRGBA(x, y, imgcolor.RGBA{over(p[2]), over(p[1]), over(p[0]), 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
