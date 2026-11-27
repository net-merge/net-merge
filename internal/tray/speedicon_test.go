package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func inkAt(img *image.NRGBA, x, y int) bool { return img.NRGBAAt(x, y).A > 0 }

// The 16 px icon, pixel for pixel: the down arrow and "126" on top, the up arrow and "7.0" below. The
// numbers end in the last column, and the arrows are 3 wide at the left edge.
func TestSpeedIconIsExactPixelArtAt16(t *testing.T) {
	want := [16]string{
		"................", // 0  margin
		".X....X..XXX.XXX", // 1  down arrow stem; "1" "2" "6"
		".X...XX....X.X..", // 2
		".X....X..XXX.XXX", // 3
		".X....X..X...X.X", // 4
		"XXX...X..X...X.X", // 5  down arrow head
		".X...XXX.XXX.XXX", // 6  down arrow tip
		"................", // 7  gap
		"................", // 8  gap
		".X.....XXX...XXX", // 9  up arrow tip; "7" "." "0"
		"XXX......X...X.X", // 10 up arrow head
		".X.......X...X.X", // 11
		".X.......X...X.X", // 12
		".X.......X...X.X", // 13
		".X.......X.X.XXX", // 14 up arrow stem; the dot
		"................", // 15 margin
	}
	img := speedIconImage("126", "7.0", true, true, 16)
	for y := 0; y < 16; y++ {
		got := make([]byte, 16)
		for x := range got {
			got[x] = '.'
			if inkAt(img, x, y) {
				got[x] = 'X'
			}
		}
		if string(got) != want[y] {
			t.Errorf("row %2d is %s, want %s", y, got, want[y])
		}
	}
}

// At a whole multiple of 16 the picture is the 16 px one, enlarged: nothing is blurred.
func TestSpeedIconScalesExactlyAtWholeMultiples(t *testing.T) {
	base := speedIconImage("126", "7.0", false, false, 16)
	for _, k := range []int{2, 3, 4} {
		big := speedIconImage("126", "7.0", false, false, 16*k)
		for y := 0; y < 16*k; y++ {
			for x := 0; x < 16*k; x++ {
				if got, want := big.NRGBAAt(x, y), base.NRGBAAt(x/k, y/k); got != want {
					t.Fatalf("size %d pixel (%d,%d) is %v, the 16 px picture has %v there", 16*k, x, y, got, want)
				}
			}
		}
	}
}

func TestSpeedIconHasTwoRowsDownOverUpWithAlignedNumbers(t *testing.T) {
	img := speedIconImage("10", "7.0", true, true, 16)
	// The wide part of an arrow is its head: near the bottom of the down arrow, near the top of the up one.
	if !inkAt(img, 0, 5) || inkAt(img, 0, 2) {
		t.Error("the top row's arrow does not point down")
	}
	if !inkAt(img, 0, 10) || inkAt(img, 0, 13) {
		t.Error("the bottom row's arrow does not point up")
	}
	// Both numbers end in the last column, whatever their length.
	for _, y := range []int{3, 11} {
		if !inkAt(img, 15, y) {
			t.Errorf("row at y=%d does not reach the right edge", y)
		}
	}
	// "10" is shorter than "7.0": the space left of it is empty where "7.0" has its 7.
	if inkAt(img, 6, 3) || inkAt(img, 6, 1) {
		t.Error(`"10" is not right aligned`)
	}
	if !inkAt(img, 7, 9) { // the top bar of the 7
		t.Error(`"7.0" is missing its 7`)
	}
}

func TestSpeedNumber(t *testing.T) {
	for in, want := range map[string]string{
		"126": "126", "7.0": "7.0", "0.2": "0.2", "10": "10", "9.9": "9.9", "999": "999",
		"1000": "999", "1234.5": "999", "12.3": "12", " 45 ": "45",
		"": "-", "x": "-", ".": "-", "--": "-",
	} {
		if got := speedNumber(in); got != want {
			t.Errorf("speedNumber(%q) = %q, want %q", in, got, want)
		}
	}
	// Whatever the tray formats, it fits: from nothing up to well past a gigabit.
	for v := 0.0; v < 1500; v += 0.05 {
		s := speedNumber(Mbps(v * 125000))
		if s == "" || textWidth(s) > maxNumberWidth {
			t.Fatalf("%.2f Mbit/s gives %q, %d wide (room for %d)", v, s, textWidth(s), maxNumberWidth)
		}
	}
}

func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrast(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Windows 11's taskbars: dark is about #202020, light about #F3F3F3.
var (
	darkTaskbar  = color.NRGBA{0x20, 0x20, 0x20, 255}
	lightTaskbar = color.NRGBA{0xF3, 0xF3, 0xF3, 255}
)

func TestSpeedInkIsReadableOnBothTaskbarsAndPausedLooksDifferent(t *testing.T) {
	for _, dark := range []bool{true, false} {
		bg := lightTaskbar
		if dark {
			bg = darkTaskbar
		}
		merging, paused := speedInk(true, dark), speedInk(false, dark)
		for name, ink := range map[string]color.NRGBA{"merging": merging, "paused": paused} {
			if c := contrast(ink, bg); c < 4.5 {
				t.Errorf("dark taskbar %v, %s: contrast %.2f, want at least 4.5", dark, name, c)
			}
		}
		if merging == paused {
			t.Errorf("dark taskbar %v: paused is drawn in the merging colour", dark)
		}
	}
	if speedInk(true, true) == speedInk(true, false) {
		t.Error("the same ink on a light and a dark taskbar")
	}
}

func TestSpeedIconStatesAndThemesDiffer(t *testing.T) {
	a := speedIconImage("126", "10", true, true, 24)
	if !bytes.Equal(a.Pix, speedIconImage("126", "10", true, true, 24).Pix) {
		t.Error("the same input drew two pictures")
	}
	for name, other := range map[string]*image.NRGBA{
		"paused":       speedIconImage("126", "10", false, true, 24),
		"light":        speedIconImage("126", "10", true, false, 24),
		"other number": speedIconImage("127", "10", true, true, 24),
	} {
		if bytes.Equal(a.Pix, other.Pix) {
			t.Errorf("%s draws the same picture", name)
		}
	}
	// The shape is the same when only the colour changes: where there is ink does not depend on it.
	p := speedIconImage("126", "10", false, true, 24)
	for i := 3; i < len(a.Pix); i += 4 {
		if a.Pix[i] != p.Pix[i] {
			t.Fatal("pausing changed the shape, not only the colour")
		}
	}
}

func TestSpeedIconAtEverySizeWindowsAsksFor(t *testing.T) {
	for _, size := range []int{12, 16, 20, 24, 28, 32, 40, 48, 64} {
		img := speedIconImage("126", "7.0", true, true, size)
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("size %d gave %v", size, b)
		}
		top, bottom, gap := 0, 0, 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if !inkAt(img, x, y) {
					continue
				}
				switch ly := float64(y) * 16 / float64(size); {
				case ly < 7:
					top++
				case ly >= 9:
					bottom++
				default:
					gap++
				}
			}
		}
		if top < size/2 || bottom < size/2 {
			t.Errorf("size %d: %d ink pixels in the top row and %d in the bottom row", size, top, bottom)
		}
		if gap != 0 && size%16 == 0 {
			t.Errorf("size %d: %d ink pixels in the gap between the rows", size, gap)
		}
	}
}

func TestSpeedIconToleratesOddInput(t *testing.T) {
	for _, in := range [][2]string{{"", ""}, {"x", "y"}, {"-1", "1e9"}, {"99999999999999999999", "."}, {"１２３", "٣"}, {"\xff\xfe", "\x00"}} {
		for _, size := range []int{0, 1, 8, 16, 33} {
			img := speedIconImage(in[0], in[1], false, false, size)
			if img.Bounds().Dx() < 8 {
				t.Errorf("size %d gave a %v picture", size, img.Bounds())
			}
		}
	}
}

func TestSpeedTooltip(t *testing.T) {
	got := speedTooltip("126", "10", true)
	for _, want := range []string{"126", "10", "Mbps", "↓", "↑"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("%q is missing %q", got, want)
		}
	}
	if p := speedTooltip("126", "10", false); !bytes.Contains([]byte(p), []byte("paused")) || p == got {
		t.Errorf("paused tooltip %q", p)
	}
	long := speedTooltip("1234567890123456789012345678901234567890", "1234567890123456789012345678901234567890", true)
	if n := len(utf16.Encode([]rune(long))); n > 63 {
		t.Errorf("a long tooltip is %d UTF-16 units, Windows 11 shows 64", n)
	}
}

// Set SPEED_PREVIEW_DIR to save a picture of the icon at 16 and 32 px on light and dark taskbars, as it is
// (and enlarged, to look at). It is not a test of anything.
func TestSpeedIconPreview(t *testing.T) {
	dir := os.Getenv("SPEED_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set SPEED_PREVIEW_DIR to save the preview")
	}
	type sample struct{ down, up string }
	samples := []sample{{"126", "10"}, {"7.0", "0.2"}}
	const pad, gap = 12, 14
	cellW := 16 + gap + 16*8 + gap + 32 + gap + 32*4 // 16 actual, 16 x8, 32 actual, 32 x4
	cellH := 32*4 + 2*pad
	rows := len(samples) * 2
	out := image.NewNRGBA(image.Rect(0, 0, pad+2*(cellW+2*pad), rows*cellH))
	blend := func(dst *image.NRGBA, x, y int, c color.NRGBA) {
		bg := dst.NRGBAAt(x, y)
		a := float64(c.A) / 255
		dst.SetNRGBA(x, y, color.NRGBA{
			uint8(float64(c.R)*a + float64(bg.R)*(1-a) + 0.5),
			uint8(float64(c.G)*a + float64(bg.G)*(1-a) + 0.5),
			uint8(float64(c.B)*a + float64(bg.B)*(1-a) + 0.5), 255})
	}
	place := func(img *image.NRGBA, ox, oy, k int) {
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				c := img.NRGBAAt(x, y)
				for dy := 0; dy < k; dy++ {
					for dx := 0; dx < k; dx++ {
						blend(out, ox+x*k+dx, oy+y*k+dy, c)
					}
				}
			}
		}
	}
	row := 0
	for _, dark := range []bool{false, true} {
		bg := lightTaskbar
		if dark {
			bg = darkTaskbar
		}
		for _, s := range samples {
			y0 := row * cellH
			for y := y0; y < y0+cellH; y++ {
				for x := 0; x < out.Bounds().Dx(); x++ {
					out.SetNRGBA(x, y, bg)
				}
			}
			for col, merging := range []bool{true, false} {
				x := pad + col*(cellW+2*pad)
				i16 := speedIconImage(s.down, s.up, merging, dark, 16)
				i32 := speedIconImage(s.down, s.up, merging, dark, 32)
				oy := y0 + pad
				place(i16, x, oy, 1)
				x += 16 + gap
				place(i16, x, oy, 8)
				x += 16*8 + gap
				place(i32, x, oy, 1)
				x += 32 + gap
				place(i32, x, oy, 4)
			}
			row++
		}
	}
	f, err := os.Create(filepath.Join(dir, "speed-icon-preview.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
}
