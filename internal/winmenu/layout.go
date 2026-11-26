package winmenu

import "math"

const baseDPI = 96

// hoverFadeMs is how long a row's highlight takes to fade in or out. The desktop's menu, recorded frame
// by frame, ramps over about this long, in a nearly straight line.
const hoverFadeMs = 83.0

// Sizes in device-independent pixels, measured from the real Windows 11 desktop context menu (build
// 26300, with UI Automation and a screenshot). They scale with the monitor's DPI, so the menu is the
// same physical size as the desktop's at 100%, 125%, 150%, 200% and so on.
const (
	dipItemHeight = 32.0
	dipPadX       = 6.4 // menu edge to row highlight
	dipPadY       = 5.6
	dipSepBand    = 8.8  // the height a separator takes, with its line in the middle
	dipIconX      = 9.6  // row edge to the check mark
	dipIconBox    = 16.0 // a row's icon, the size of the desktop menu's icons
	dipTextX      = 36.8 // row edge to the text
	dipTextRight  = 10.4 // text to row edge, when no row has a submenu
	dipSubGap     = 16.0 // text to submenu arrow
	dipChevron    = 12.0
	dipChevronR   = 9.6 // submenu arrow to row edge
	dipRowRadius  = 4.0 // the row highlight's corners; the window's 8 come from DWM
	dipFont       = 14.0
	dipMinWidth   = 156.0
	dipSubOverlap = 4.0  // a submenu sits slightly over its parent
	dipEdgeGap    = 12.0 // the menu to the taskbar and to the screen's edge, as the Quick Settings flyout
)

// px converts a size to device pixels at dpi.
func px(dip float64, dpi int) int { return int(math.Round(dip * float64(dpi) / baseDPI)) }

// hairline is a separator's thickness: at least one pixel, and two at 200%.
func hairline(dpi int) int { return max(1, dpi/baseDPI) }

// row is one line of an open menu.
type row struct {
	item                           *Item // nil for a separator
	title                          string
	icon                           string // "" for a row whose left column holds its check mark
	sep, checked, disabled, hasSub bool
	checkable                      bool
	y, h                           int
	glow                           float64 // how lit its highlight is, from 0 to 1
}

// easeOut is the curve of a fade-in: fast at first, settling gently, as WinUI's entrances.
func easeOut(t float64) float64 {
	t = math.Min(math.Max(t, 0), 1)
	return 1 - (1-t)*(1-t)*(1-t)
}

type rect struct{ left, top, right, bottom int }

func (r rect) contains(x, y int) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

// layoutRows sets each row's position and returns the menu's outer size. minW keeps an open menu from
// shrinking as its live numbers change, and maxW keeps it on the monitor.
func layoutRows(rows []row, dpi, minW, maxW int, textWidth func(string) int) (w, h int) {
	padX, padY := px(dipPadX, dpi), px(dipPadY, dpi)
	y, widest, anySub := padY, 0, false
	for i := range rows {
		r := &rows[i]
		r.y = y
		if r.sep {
			r.h = px(dipSepBand, dpi)
		} else {
			r.h = px(dipItemHeight, dpi)
			widest = max(widest, textWidth(r.title))
			// A row with an icon shows its check mark where a submenu arrow would be.
			anySub = anySub || r.hasSub || (r.checkable && r.icon != "")
		}
		y += r.h
	}
	tail := px(dipTextRight, dpi)
	if anySub {
		tail = px(dipSubGap, dpi) + px(dipChevron, dpi) + px(dipChevronR, dpi)
	}
	w = max(2*padX+px(dipTextX, dpi)+widest+tail, px(dipMinWidth, dpi), minW)
	if maxW > 0 {
		w = min(w, maxW)
	}
	return w, y + padY
}

// placeRoot opens a menu of size w×h in the corner where the taskbar meets the screen's right edge
// (the bottom edge for a taskbar on the left or right), gap pixels from both, as Windows 11's own
// tray flyouts sit. area is the work area less that gap: the menu stays inside it as it grows.
func placeRoot(cx, cy, w, h int, work, monitor rect, gap int) (x, y int, area rect) {
	area = rect{work.left + gap, work.top + gap, work.right - gap, work.bottom - gap}
	switch taskbarSide(cx, cy, work, monitor) {
	case sideTop:
		x, y = area.right-w, area.top
	case sideLeft:
		x, y = area.left, area.bottom-h
	default: // bottom or right
		x, y = area.right-w, area.bottom-h
	}
	x, y = clampInto(x, y, w, h, area)
	return x, y, area
}

const (
	sideBottom = iota
	sideTop
	sideLeft
	sideRight
)

// taskbarSide is the monitor edge the taskbar is on: the one the work area leaves room for, or, with
// a taskbar that hides itself, the edge the pointer is nearest to.
func taskbarSide(cx, cy int, work, monitor rect) int {
	switch {
	case work.bottom < monitor.bottom:
		return sideBottom
	case work.top > monitor.top:
		return sideTop
	case work.left > monitor.left:
		return sideLeft
	case work.right < monitor.right:
		return sideRight
	}
	side, best := sideBottom, monitor.bottom-cy
	for _, c := range []struct{ side, dist int }{{sideTop, cy - monitor.top}, {sideLeft, cx - monitor.left}, {sideRight, monitor.right - cx}} {
		if c.dist < best {
			side, best = c.side, c.dist
		}
	}
	return side
}

// placeSub opens a submenu beside its parent's row, or on the other side if it would leave the monitor.
func placeSub(parent rect, rowTop, w, h, dpi int, work rect) (x, y int) {
	overlap := px(dipSubOverlap, dpi)
	x = parent.right - overlap
	if x+w > work.right {
		x = parent.left - w + overlap
	}
	return clampInto(x, parent.top+rowTop-px(dipPadY, dpi), w, h, work)
}

func clampInto(x, y, w, h int, work rect) (int, int) {
	x = max(min(x, work.right-w), work.left)
	y = max(min(y, work.bottom-h), work.top)
	return x, y
}

// canvas is a 32-bit premultiplied BGRA image, which is what DWM composites over the backdrop.
type canvas struct {
	pix  []byte
	w, h int
}

// color is straight, not premultiplied; a is the opacity from 0 to 1.
type color struct {
	r, g, b uint8
	a       float64
}

// blend paints c over one pixel with the given coverage (0 to 1).
func (cv canvas) blend(x, y int, c color, coverage float64) {
	a := c.a * coverage
	if a <= 0 {
		return
	}
	p := cv.pix[(y*cv.w+x)*4 : (y*cv.w+x)*4+4]
	for i, s := range [4]float64{float64(c.b), float64(c.g), float64(c.r), 255} {
		p[i] = uint8(math.Round(s*a + float64(p[i])*(1-a)))
	}
}

// fill sets every pixel to c. Clearing to c.a == 0 leaves the backdrop showing untouched.
func (cv canvas) fill(c color) {
	px := [4]byte{
		uint8(math.Round(float64(c.b) * c.a)),
		uint8(math.Round(float64(c.g) * c.a)),
		uint8(math.Round(float64(c.r) * c.a)),
		uint8(math.Round(255 * c.a)),
	}
	for i := 0; i+4 <= len(cv.pix); i += 4 {
		copy(cv.pix[i:i+4], px[:])
	}
}

// fillRoundRect fills a rectangle with antialiased corners.
func (cv canvas) fillRoundRect(r rect, radius float64, c color) {
	cx, cy := float64(r.left+r.right)/2, float64(r.top+r.bottom)/2
	inX, inY := float64(r.right-r.left)/2-radius, float64(r.bottom-r.top)/2-radius
	for y := max(r.top, 0); y < min(r.bottom, cv.h); y++ {
		for x := max(r.left, 0); x < min(r.right, cv.w); x++ {
			dx := math.Max(math.Abs(float64(x)+0.5-cx)-inX, 0)
			dy := math.Max(math.Abs(float64(y)+0.5-cy)-inY, 0)
			cov := 0.5 - (math.Hypot(dx, dy) - radius)
			cv.blend(x, y, c, math.Min(math.Max(cov, 0), 1))
		}
	}
}

// palette is the menu's colours over the system's light or dark acrylic.
type palette struct {
	base, text, muted, hover, sep color
	level                         uint8 // when set, what fillLevel writes for base's colour channels
}

// darkBase goes over DWM's dark acrylic when a dark menu can't draw its own (see darkAcrylic). DWM leaves
// it far too light and see-through: measured over black, grey and white it gives 51, 95 and 139, and 84
// when the window is inactive. This layer brings it near the desktop menu's shade, though not its colour.
var darkBase = color{26, 26, 26, 0.745}

// lightBase goes over a plain blur. The desktop's menu, measured after a real right-click, is 214 over
// black, 233 over mid-grey and 252 over white. The blur passes about 85% of what is behind it, so that
// takes a layer brighter than white at this opacity, which only premultiplied pixels can hold: see
// lightLevel, its colour channels as written.
var lightBase = color{255, 255, 255, 0.825}

const lightLevel = 214

// fillLevel is fill for a layer whose colour channels are written directly, and may exceed its alpha.
func (cv canvas) fillLevel(level uint8, a float64) {
	alpha := uint8(math.Round(255 * a))
	for i := 0; i+4 <= len(cv.pix); i += 4 {
		cv.pix[i], cv.pix[i+1], cv.pix[i+2], cv.pix[i+3] = level, level, level, alpha
	}
}

// Dark acrylic as the desktop's menu has it, which no DWM backdrop can produce: the background keeps its
// colour but takes a fixed luminosity, under a thin tint. These are the values of WinUI's dark acrylic,
// and they reproduce the desktop's menu measured after a real right-click, to within a level.
const (
	darkTint       = 44.0 // #2C2C2C
	darkTintAlpha  = 0.15
	darkLumAlpha   = 0.96
	darkBlurDip    = 30.0 // the blur's standard deviation
	backdropMargin = 3    // how many standard deviations of the screen around a menu feed its blur
)

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

// withLum is the W3C "luminosity" blend: the colour's hue and saturation at luminosity l.
func withLum(c [3]float64, l float64) [3]float64 {
	d := l - lum(c)
	out := [3]float64{c[0] + d, c[1] + d, c[2] + d}
	lo, hi := math.Min(out[0], math.Min(out[1], out[2])), math.Max(out[0], math.Max(out[1], out[2]))
	for i := range out {
		if lo < 0 {
			out[i] = l + (out[i]-l)*l/(l-lo)
		} else if hi > 255 {
			out[i] = l + (out[i]-l)*(255-l)/(hi-l)
		}
	}
	return out
}

// darkAcrylic is what the desktop's dark menu shows over a blurred background colour.
func darkAcrylic(b [3]float64) [3]float64 {
	l := withLum(b, darkTint)
	var out [3]float64
	for i := range out {
		mixed := darkLumAlpha*l[i] + (1-darkLumAlpha)*b[i]
		out[i] = darkTintAlpha*darkTint + (1-darkTintAlpha)*mixed
	}
	return out
}

// backdrop is a menu's background, worked out from a capture of the screen behind it. It is held at low
// resolution, as it is blurred anyway, and in screen coordinates, so a menu that grows while open still
// finds its colours.
type backdrop struct {
	x, y, w, h, step int
	cells            [][3]float32
}

// newBackdrop blurs a capture whose top-left is at screen position (x, y) and applies the recipe.
func newBackdrop(src canvas, x, y int, sigma float64) *backdrop {
	const cellsPerSigma = 3
	step := max(1, int(math.Round(sigma/cellsPerSigma)))
	w, h := (src.w+step-1)/step, (src.h+step-1)/step
	cur := make([][3]float64, w*h)
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < w; cx++ {
			var sum [3]float64
			n := 0.0
			for py := cy * step; py < min((cy+1)*step, src.h); py++ {
				for px := cx * step; px < min((cx+1)*step, src.w); px++ {
					p := src.pix[(py*src.w+px)*4:]
					sum[0], sum[1], sum[2] = sum[0]+float64(p[2]), sum[1]+float64(p[1]), sum[2]+float64(p[0])
					n++
				}
			}
			cur[cy*w+cx] = [3]float64{sum[0] / n, sum[1] / n, sum[2] / n}
		}
	}
	// Three box blurs make a very close Gaussian: their variance is 3((2r+1)^2-1)/12 cells.
	cellSigma := sigma / float64(step)
	r := max(1, int(math.Round((math.Sqrt(4*cellSigma*cellSigma+1)-1)/2)))
	next := make([][3]float64, len(cur))
	box := func(n, stride, lines, lineStride int) {
		for line := 0; line < lines; line++ {
			at := func(i int) [3]float64 { return cur[line*lineStride+max(0, min(i, n-1))*stride] }
			var sum [3]float64
			for i := -r; i <= r; i++ {
				c := at(i)
				sum[0], sum[1], sum[2] = sum[0]+c[0], sum[1]+c[1], sum[2]+c[2]
			}
			width := float64(2*r + 1)
			for i := 0; i < n; i++ {
				next[line*lineStride+i*stride] = [3]float64{sum[0] / width, sum[1] / width, sum[2] / width}
				in, out := at(i+r+1), at(i-r)
				sum[0], sum[1], sum[2] = sum[0]+in[0]-out[0], sum[1]+in[1]-out[1], sum[2]+in[2]-out[2]
			}
		}
		cur, next = next, cur
	}
	for range 3 {
		box(w, 1, h, w)
		box(h, w, w, 1)
	}
	b := &backdrop{x: x, y: y, w: w, h: h, step: step, cells: make([][3]float32, w*h)}
	for i, c := range cur {
		a := darkAcrylic(c)
		b.cells[i] = [3]float32{float32(a[0]), float32(a[1]), float32(a[2])}
	}
	return b
}

// paint fills an opaque BGRA image whose top-left is at screen position (x, y), interpolating the cells
// and repeating the outermost ones past the edge of the capture.
func (b *backdrop) paint(pix []byte, x, y, w, h int) {
	for py := 0; py < h; py++ {
		fy := (float32(y+py-b.y)+0.5)/float32(b.step) - 0.5
		y0 := int(math.Floor(float64(fy)))
		ty := fy - float32(y0)
		y1 := max(0, min(y0+1, b.h-1))
		y0 = max(0, min(y0, b.h-1))
		for px := 0; px < w; px++ {
			fx := (float32(x+px-b.x)+0.5)/float32(b.step) - 0.5
			x0 := int(math.Floor(float64(fx)))
			tx := fx - float32(x0)
			x1 := max(0, min(x0+1, b.w-1))
			x0 = max(0, min(x0, b.w-1))
			c00, c10, c01, c11 := &b.cells[y0*b.w+x0], &b.cells[y0*b.w+x1], &b.cells[y1*b.w+x0], &b.cells[y1*b.w+x1]
			i := (py*w + px) * 4
			for ch := 0; ch < 3; ch++ {
				top := c00[ch] + (c10[ch]-c00[ch])*tx
				bottom := c01[ch] + (c11[ch]-c01[ch])*tx
				pix[i+2-ch] = uint8(top + (bottom-top)*ty + 0.5)
			}
			pix[i+3] = 255
		}
	}
}

// stepGlow moves a row's highlight from cur towards target for dtMs of its fade, without overshooting.
func stepGlow(cur, target, dtMs float64) float64 {
	step := dtMs / hoverFadeMs
	if cur < target {
		return math.Min(cur+step, target)
	}
	return math.Max(cur-step, target)
}

func paletteFor(dark bool) palette {
	if dark {
		return palette{
			base:  darkBase,
			text:  color{255, 255, 255, 1},
			muted: color{255, 255, 255, 0.6},
			hover: color{255, 255, 255, 0.0605},
			sep:   color{255, 255, 255, 0.0837},
		}
	}
	return palette{
		base:  lightBase,
		level: lightLevel,
		text:  color{0, 0, 0, 0.9},
		muted: color{0, 0, 0, 0.55},
		hover: color{0, 0, 0, 0.0373},
		sep:   color{0, 0, 0, 0.0803},
	}
}
