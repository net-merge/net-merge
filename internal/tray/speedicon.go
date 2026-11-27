package tray

import (
	"image"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The speed icon is a second tray icon beside the app's: two rows, an arrow and a number each, the
// download on top and the upload below. This file only draws it, as a pure function of what to show, so
// it can be tested and previewed anywhere. speed_windows.go puts the result in the notification area.
//
// It is drawn on a 16 x 16 grid of "logical pixels" and scaled to the icon size Windows asks for: 16 px at
// 100%, 20 at 125%, 24 at 150%, 32 at 200%. Each logical pixel covers the pixels it overlaps by the share
// it overlaps them, so at a whole multiple of 16 the picture is exactly the 16 px one, enlarged, and
// between them the edges are only slightly soft.

const (
	speedGrid   = 16 // the icon is drawn as if it were this many pixels square
	glyphHeight = 6  // digits and arrows are 6 logical pixels tall...
	glyphWidth  = 3  // ...and 3 wide
	glyphGap    = 1  // between two glyphs
	arrowWidth  = glyphWidth
	// A row is an arrow, a gap and at most this many logical pixels of number: "126" is 3*3+2 = 11.
	maxNumberWidth = speedGrid - arrowWidth - glyphGap - 1
)

// Rows of the two rows: where each one starts. 6 + 2 + 6 + margins of 1 = 16.
var rowTop = [2]int{1, 9}

// The glyphs, "X" being ink. Digits are 3 wide; the dot is 1 wide.
var glyphs = map[rune][glyphHeight]string{
	'0': {"XXX", "X.X", "X.X", "X.X", "X.X", "XXX"},
	'1': {".X.", "XX.", ".X.", ".X.", ".X.", "XXX"},
	'2': {"XXX", "..X", "XXX", "X..", "X..", "XXX"},
	'3': {"XXX", "..X", "XXX", "..X", "..X", "XXX"},
	'4': {"X.X", "X.X", "X.X", "XXX", "..X", "..X"},
	'5': {"XXX", "X..", "XXX", "..X", "..X", "XXX"},
	'6': {"XXX", "X..", "XXX", "X.X", "X.X", "XXX"},
	'7': {"XXX", "..X", "..X", "..X", "..X", "..X"},
	'8': {"XXX", "X.X", "XXX", "X.X", "X.X", "XXX"},
	'9': {"XXX", "X.X", "XXX", "..X", "..X", "XXX"},
	'.': {".", ".", ".", ".", ".", "X"},
	'-': {"...", "...", "XXX", "...", "...", "..."},
}

var (
	arrowDown = [glyphHeight]string{".X.", ".X.", ".X.", ".X.", "XXX", ".X."}
	arrowUp   = [glyphHeight]string{".X.", "XXX", ".X.", ".X.", ".X.", ".X."}
)

// speedNumber makes the text drawn for a speed as it comes from mbps: digits and a dot, at most
// maxNumberWidth wide. A speed too big to fit (1000 Mbit/s and over) shows as 999: the tooltip has it all.
func speedNumber(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' {
			b.WriteRune(r)
		}
	}
	text := b.String()
	if text == "" || text == "." {
		return "-"
	}
	if v, err := strconv.ParseFloat(text, 64); err == nil && v >= 999.5 {
		return "999"
	}
	for textWidth(text) > maxNumberWidth && len(text) > 1 {
		text = text[:len(text)-1]
	}
	return strings.TrimSuffix(text, ".")
}

// textWidth is how many logical pixels a number takes.
func textWidth(text string) int {
	w := 0
	for i, r := range text {
		if i > 0 {
			w += glyphGap
		}
		w += len(glyphs[r][0])
	}
	return w
}

// speedInk is the colour the icon is drawn in. It is the taskbar's own ink while merging (white on a dark
// taskbar, near black on a light one) and amber while paused. Both keep a contrast of 4.5 : 1 or more with
// the taskbar's colour, which the tests check.
func speedInk(merging, darkTaskbar bool) color.NRGBA {
	switch {
	case merging && darkTaskbar:
		return color.NRGBA{255, 255, 255, 255}
	case merging:
		return color.NRGBA{0x1B, 0x1B, 0x1B, 255}
	case darkTaskbar:
		return color.NRGBA{0xFF, 0xC8, 0x3D, 255}
	}
	return color.NRGBA{0x9A, 0x52, 0x00, 255}
}

// speedIconImage draws the icon, size pixels square: an arrow down and the download, an arrow up and the
// upload. down and up are numbers as the tray writes them ("126", "7.0"). The numbers are right aligned so
// their decimal points line up.
func speedIconImage(down, up string, merging, darkTaskbar bool, size int) *image.NRGBA {
	if size < 8 {
		size = 8
	}
	cov := make([]float64, size*size) // how much of each pixel is ink, 0 to 1
	scale := float64(size) / speedGrid
	cell := func(cx, cy int) { // a logical pixel
		x0, x1 := float64(cx)*scale, float64(cx+1)*scale
		y0, y1 := float64(cy)*scale, float64(cy+1)*scale
		for py := int(y0); py < size && float64(py) < y1; py++ {
			oy := min(y1, float64(py+1)) - max(y0, float64(py))
			for px := int(x0); px < size && float64(px) < x1; px++ {
				ox := min(x1, float64(px+1)) - max(x0, float64(px))
				cov[py*size+px] = min(cov[py*size+px]+ox*oy, 1)
			}
		}
	}
	draw := func(g [glyphHeight]string, x, y int) {
		for gy, line := range g {
			for gx, c := range line {
				if c == 'X' {
					cell(x+gx, y+gy)
				}
			}
		}
	}
	for i, row := range []struct {
		arrow  [glyphHeight]string
		number string
	}{{arrowDown, speedNumber(down)}, {arrowUp, speedNumber(up)}} {
		draw(row.arrow, 0, rowTop[i])
		x := speedGrid - textWidth(row.number)
		for _, r := range row.number {
			draw(glyphs[r], x, rowTop[i])
			x += len(glyphs[r][0]) + glyphGap
		}
	}
	ink := speedInk(merging, darkTaskbar)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i, c := range cov {
		if c <= 0 {
			continue
		}
		o := i * 4
		img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = ink.R, ink.G, ink.B, uint8(c*255+0.5)
	}
	return img
}

// speedTooltip is the icon's tooltip: the same numbers in full, with their unit. Windows 11 shows the
// first 64 characters of a tooltip, so this stays well within them.
func speedTooltip(down, up string, merging bool) string {
	state := "net-merge"
	if !merging {
		state = "net-merge, paused"
	}
	s := state + ": ↓" + strings.TrimSpace(down) + " ↑" + strings.TrimSpace(up) + " Mbps"
	if r := utf16.Encode([]rune(s)); len(r) > 63 {
		s = string(utf16.Decode(r[:63]))
	}
	return s
}
