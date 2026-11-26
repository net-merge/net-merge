package winmenu

import "testing"

// The desktop's dark menu, measured after a real right-click over each flat colour.
func TestDarkAcrylicMatchesTheDesktopMenu(t *testing.T) {
	for _, c := range []struct{ behind, want [3]float64 }{
		{[3]float64{0, 0, 0}, [3]float64{42, 42, 42}},
		{[3]float64{128, 128, 128}, [3]float64{47, 47, 47}},
		{[3]float64{255, 255, 255}, [3]float64{52, 52, 52}},
		{[3]float64{200, 60, 40}, [3]float64{110, 21, 8}},
	} {
		got := darkAcrylic(c.behind)
		for i := range got {
			if d := got[i] - c.want[i]; d < -1.5 || d > 1.5 {
				t.Errorf("over %v: got %.1f, the desktop's menu is %v", c.behind, got, c.want)
				break
			}
		}
	}
}

func TestBackdropBlursAndPaints(t *testing.T) {
	const w, h = 200, 40
	src := canvas{pix: make([]byte, w*h*4), w: w, h: h}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ { // red on the left half, blue on the right
			p := src.pix[(y*w+x)*4:]
			if x < w/2 {
				p[2] = 200
			} else {
				p[0] = 200
			}
		}
	}
	const atX, atY = 1000, 500
	b := newBackdrop(src, atX, atY, 10)
	out := make([]byte, w*h*4)
	b.paint(out, atX, atY, w, h)
	at := func(x int) []byte { return out[(h/2*w+x)*4:] }
	if at(0)[3] != 255 {
		t.Error("the backdrop should be opaque")
	}
	if !(at(0)[2] > at(0)[0]+50 && at(w - 1)[0] > at(w - 1)[2]+50) {
		t.Errorf("the far ends should keep their colours: left %v, right %v", at(0)[:3], at(w - 1)[:3])
	}
	mid := at(w / 2)
	if d := int(mid[0]) - int(mid[2]); d < -12 || d > 12 {
		t.Errorf("the middle should blend evenly: %v", mid[:3])
	}
	// A menu that has grown past the capture repeats its edge rather than reading out of bounds.
	wide := make([]byte, (w+40)*h*4)
	b.paint(wide, atX-20, atY, w+40, h)
	if wide[3] != 255 || wide[(w+39)*4+3] != 255 {
		t.Error("painting past the capture should still fill the pixels")
	}
}

func TestStepGlow(t *testing.T) {
	half := hoverFadeMs / 2
	if g := stepGlow(0, 1, half); g != 0.5 {
		t.Errorf("half way in: %v", g)
	}
	if g := stepGlow(1, 0, half); g != 0.5 {
		t.Errorf("half way out: %v", g)
	}
	if g := stepGlow(0.9, 1, hoverFadeMs); g != 1 {
		t.Errorf("fading in overshot: %v", g)
	}
	if g := stepGlow(0.1, 0, hoverFadeMs); g != 0 {
		t.Errorf("fading out overshot: %v", g)
	}
	if g := stepGlow(1, 1, half); g != 1 {
		t.Errorf("a row already there moved: %v", g)
	}
}

func testRows() []row {
	return []row{
		{item: &Item{}, title: "status", disabled: true},
		{sep: true},
		{item: &Item{}, title: "Wi-Fi", hasSub: true},
		{sep: true},
		{item: &Item{}, title: "Quit"},
	}
}

// fixedWidth stands in for a real font, whose width scales with the DPI.
func fixedWidth(dpi int) func(string) int {
	return func(s string) int { return px(float64(len(s))*7, dpi) }
}

// The row height and insets the desktop's menu was measured with, at the scale it was measured at.
func TestMetricsMatchTheMeasuredDesktopMenu(t *testing.T) {
	const dpi = 120 // 125%
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"row height", px(dipItemHeight, dpi), 40},
		{"side inset", px(dipPadX, dpi), 8},
		{"top inset", px(dipPadY, dpi), 7},
		{"separator band", px(dipSepBand, dpi), 11},
		{"check mark x", px(dipIconX, dpi), 12},
		{"check mark box", px(dipIconBox, dpi), 20},
		{"text x", px(dipTextX, dpi), 46},
		{"text to row edge", px(dipTextRight, dpi), 13},
	} {
		if c.got != c.want {
			t.Errorf("%s at 125%% is %d px, the desktop menu has %d", c.name, c.got, c.want)
		}
	}
}

func TestScalesExactly(t *testing.T) {
	for _, c := range []struct{ dpi, row, font, line int }{
		{96, 32, 14, 1},  // 1080p at 100%
		{120, 40, 18, 1}, // 125%
		{144, 48, 21, 1}, // 150%
		{192, 64, 28, 2}, // 4K at 200%
	} {
		if got := px(dipItemHeight, c.dpi); got != c.row {
			t.Errorf("row at %d DPI = %d, want %d", c.dpi, got, c.row)
		}
		if got := px(dipFont, c.dpi); got != c.font {
			t.Errorf("font at %d DPI = %d, want %d", c.dpi, got, c.font)
		}
		if got := hairline(c.dpi); got != c.line {
			t.Errorf("separator at %d DPI = %d, want %d", c.dpi, got, c.line)
		}
	}
}

// 200% on 4K must be the same physical menu as 100% on 1080p: twice the pixels, within rounding.
func Test200PercentIsTwice100Percent(t *testing.T) {
	r1, r2 := testRows(), testRows()
	w1, h1 := layoutRows(r1, 96, 0, 1920, fixedWidth(96))
	w2, h2 := layoutRows(r2, 192, 0, 3840, fixedWidth(192))
	const slack = 3 // each rounded inset can differ by a pixel
	if d := w2 - 2*w1; d < -slack || d > slack {
		t.Errorf("width: %d at 100%%, %d at 200%%", w1, w2)
	}
	if d := h2 - 2*h1; d < -slack || d > slack {
		t.Errorf("height: %d at 100%%, %d at 200%%", h1, h2)
	}
	for i := range r1 {
		if !r1[i].sep && r2[i].h != 2*r1[i].h {
			t.Errorf("row %d: %d at 100%%, %d at 200%%", i, r1[i].h, r2[i].h)
		}
	}
}

func TestRowsDoNotOverlapAndFitTheMenu(t *testing.T) {
	for _, dpi := range []int{96, 120, 144, 168, 192, 288} {
		rows := testRows()
		_, h := layoutRows(rows, dpi, 0, 0, fixedWidth(dpi))
		y := px(dipPadY, dpi)
		for i, r := range rows {
			if r.y != y {
				t.Errorf("dpi %d row %d starts at %d, want %d", dpi, i, r.y, y)
			}
			y += r.h
		}
		if h != y+px(dipPadY, dpi) {
			t.Errorf("dpi %d: height %d, rows end at %d", dpi, h, y)
		}
	}
}

// The menu sits in the corner by the taskbar, the same gap from the taskbar and the screen's edge.
func TestPlacedInTheCornerWithEqualGaps(t *testing.T) {
	for _, c := range []struct {
		name          string
		dpi           int
		work, monitor rect
		cx, cy        int
		wantX, wantY  string // which edges it hugs
	}{
		{"1080p 100%", 96, rect{0, 0, 1920, 1032}, rect{0, 0, 1920, 1080}, 1800, 1060, "right", "bottom"},
		{"4K 200%", 192, rect{0, 0, 3840, 2064}, rect{0, 0, 3840, 2160}, 3600, 2120, "right", "bottom"},
		{"second monitor left of the main one", 96, rect{-1920, 0, 0, 1032}, rect{-1920, 0, 0, 1080}, -40, 1060, "right", "bottom"},
		{"top taskbar 4K 200%", 192, rect{0, 96, 3840, 2160}, rect{0, 0, 3840, 2160}, 3600, 40, "right", "top"},
		{"left taskbar", 96, rect{48, 0, 1920, 1080}, rect{0, 0, 1920, 1080}, 20, 1000, "left", "bottom"},
		{"hidden taskbar at the bottom", 96, rect{0, 0, 1920, 1080}, rect{0, 0, 1920, 1080}, 1800, 1078, "right", "bottom"},
	} {
		w, h := layoutRows(testRows(), c.dpi, 0, c.work.right-c.work.left, fixedWidth(c.dpi))
		gap := px(dipEdgeGap, c.dpi)
		x, y, area := placeRoot(c.cx, c.cy, w, h, c.work, c.monitor, gap)
		if x < area.left || x+w > area.right || y < area.top || y+h > area.bottom {
			t.Errorf("%s: menu at %d,%d size %dx%d leaves %+v", c.name, x, y, w, h, area)
		}
		gapX, gapY := c.work.right-(x+w), c.work.bottom-(y+h)
		if c.wantX == "left" {
			gapX = x - c.work.left
		}
		if c.wantY == "top" {
			gapY = y - c.work.top
		}
		if gapX != gap || gapY != gap {
			t.Errorf("%s: gaps %d (%s) and %d (%s), want both %d", c.name, gapX, c.wantX, gapY, c.wantY, gap)
		}
	}
}

func TestSubmenuFlipsAtTheMonitorEdge(t *testing.T) {
	work := rect{0, 0, 3840, 2064}
	parent := rect{3200, 1500, 3800, 2064}
	x, _ := placeSub(parent, 100, 400, 300, 192, work)
	if x+400 > work.right || x >= parent.left {
		t.Errorf("submenu at x=%d should open left of its parent", x)
	}
	parent = rect{100, 100, 700, 700}
	if x, _ := placeSub(parent, 100, 400, 300, 192, work); x <= parent.left {
		t.Errorf("submenu at x=%d should open right of its parent", x)
	}
}

func TestOpenMenuDoesNotShrink(t *testing.T) {
	first, _ := layoutRows(testRows(), 96, 0, 0, fixedWidth(96))
	if w, _ := layoutRows(testRows(), 96, first+50, 0, fixedWidth(96)); w != first+50 {
		t.Errorf("width %d, want it held at %d", w, first+50)
	}
}

func TestSnapshotDropsSeparatorsWithNothingToDivide(t *testing.T) {
	hidden := &Item{title: "hidden", hidden: true}
	rows := snapshot([]*Item{
		{sep: true}, {title: "a"}, {sep: true}, hidden, {sep: true}, {title: "b"}, {sep: true},
	})
	if len(rows) != 3 || rows[0].title != "a" || !rows[1].sep || rows[2].title != "b" {
		t.Fatalf("got %+v", rows)
	}
}

func TestClickNeverBlocks(t *testing.T) {
	it := newItem("x", false)
	it.click()
	it.click() // nobody is reading: must not block
	<-it.ClickedCh
	select {
	case <-it.ClickedCh:
		t.Fatal("the second click should have been dropped")
	default:
	}
}

func TestRoundedRectIsPremultipliedAndStaysInside(t *testing.T) {
	cv := canvas{pix: make([]byte, 20*20*4), w: 20, h: 20}
	cv.fillRoundRect(rect{2, 2, 18, 18}, 4, color{255, 255, 255, 0.5})
	at := func(x, y int) []byte { return cv.pix[(y*20+x)*4 : (y*20+x)*4+4] }
	if a := at(0, 0)[3]; a != 0 {
		t.Errorf("outside the rectangle alpha = %d", a)
	}
	if a := at(2, 2)[3]; a != 0 {
		t.Errorf("the corner should be cut off, alpha = %d", a)
	}
	mid := at(10, 10)
	if mid[3] != 128 || mid[0] != mid[3] {
		t.Errorf("middle = %v, want premultiplied 50%% white", mid)
	}
}
