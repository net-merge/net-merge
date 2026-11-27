package tray

import (
	"image/color"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pGetGuiResources = user32.NewProc("GetGuiResources")
	pGetIconInfo     = user32.NewProc("GetIconInfo")
	gdi32            = windows.NewLazySystemDLL("gdi32.dll")
	pGetObject       = gdi32.NewProc("GetObjectW")
	pDeleteObject    = gdi32.NewProc("DeleteObject")
)

// guiHandles is how many GDI and USER objects this process holds: the two counts that an icon that is
// never destroyed would raise.
func guiHandles() (gdi, user int) {
	p := uintptr(windows.CurrentProcess())
	g, _, _ := pGetGuiResources.Call(p, 0) // GR_GDIOBJECTS
	u, _, _ := pGetGuiResources.Call(p, 1) // GR_USEROBJECTS
	return int(g), int(u)
}

func TestNotifyIconDataHasTheSizeWindowsExpects(t *testing.T) {
	// NOTIFYICONDATAW, Windows Vista and later: 976 bytes on 64-bit Windows, on x64 and on ARM64.
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Errorf("notifyIconData is %d bytes, want 976", got)
	}
	if got := unsafe.Sizeof(msg{}); got != 48 {
		t.Errorf("MSG is %d bytes, want 48", got)
	}
}

// What Windows makes of the picture: an icon of the size asked for, with an alpha channel.
func TestIconFromPictureIsTheSizeAskedForWithAlpha(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32, 48} {
		h := hiconFromImage(speedIconImage("126", "7.0", true, true, size))
		if h == 0 {
			t.Fatalf("size %d: no icon", size)
		}
		var ii struct {
			icon, xHot, yHot uint32
			mask, color      uintptr
		}
		if r, _, _ := pGetIconInfo.Call(h, uintptr(unsafe.Pointer(&ii))); r == 0 {
			pDestroyIcon.Call(h)
			t.Fatalf("size %d: GetIconInfo failed", size)
		}
		var bm struct {
			kind, width, height, widthBytes int32
			planes, bitsPixel               uint16
			bits                            uintptr
		}
		pGetObject.Call(ii.color, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
		pDeleteObject.Call(ii.color) // GetIconInfo hands over copies of the bitmaps
		pDeleteObject.Call(ii.mask)
		pDestroyIcon.Call(h)
		if int(bm.width) != size || int(bm.height) != size || bm.bitsPixel != 32 {
			t.Errorf("size %d: Windows made a %d x %d icon of %d bits", size, bm.width, bm.height, bm.bitsPixel)
		}
	}
}

// Each new picture is a new HICON, and the one it replaces is destroyed. This is that cycle, thousands of
// times, with the two handle counts watched. (speed_live_windows_test.go does it through the real tray.)
func TestMakingAndDestroyingIconsDoesNotLeakHandles(t *testing.T) {
	cycle := func(i int) {
		h := hiconFromImage(speedIconImage(Mbps(float64(i%1500)*125000), "7.0", i%2 == 0, i%3 == 0, 16+4*(i%5)))
		if h == 0 {
			t.Fatalf("no icon at update %d", i)
		}
		pDestroyIcon.Call(h)
	}
	for i := 0; i < 100; i++ { // warm up: Windows and Go allocate what they keep on first use
		cycle(i)
	}
	gdi0, user0 := guiHandles()
	for i := 0; i < 5000; i++ {
		cycle(i)
	}
	gdi1, user1 := guiHandles()
	t.Logf("GDI objects %d -> %d, USER objects %d -> %d over 5000 icons", gdi0, gdi1, user0, user1)
	if gdi1-gdi0 > 2 || user1-user0 > 2 {
		t.Errorf("handles grew: GDI %d -> %d, USER %d -> %d", gdi0, gdi1, user0, user1)
	}
}

// What the notification area does with an icon is draw it over the taskbar. This does the same with
// Windows' own DrawIconEx, over each taskbar colour and at the sizes of 100% to 200%, and compares the
// result with the picture blended over that colour. It catches an icon with its alpha mishandled, such as
// edges that come out dark or light, or an icon that is empty.
func TestWindowsDrawsTheIconAsPictured(t *testing.T) {
	pCreateCompatibleDC := gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection := gdi32.NewProc("CreateDIBSection")
	pSelectObject := gdi32.NewProc("SelectObject")
	pDeleteDC := gdi32.NewProc("DeleteDC")
	pGdiFlush := gdi32.NewProc("GdiFlush")
	pDrawIconEx := user32.NewProc("DrawIconEx")

	for _, c := range []struct {
		name    string
		merging bool
		dark    bool
		bg      color.NRGBA
	}{
		{"merging, dark taskbar", true, true, darkTaskbar},
		{"paused, dark taskbar", false, true, darkTaskbar},
		{"merging, light taskbar", true, false, lightTaskbar},
		{"paused, light taskbar", false, false, lightTaskbar},
	} {
		for _, size := range []int{16, 20, 24, 32, 48} {
			img := speedIconImage("126", "7.0", c.merging, c.dark, size)
			h := hiconFromImage(img)
			if h == 0 {
				t.Fatalf("%s, %d px: no icon", c.name, size)
			}
			bi := struct {
				size          uint32
				width, height int32
				planes, bits  uint16
				comp, sizeImg uint32
				xp, yp        int32
				used, imp     uint32
			}{size: 40, width: int32(size), height: -int32(size), planes: 1, bits: 32}
			var bits unsafe.Pointer
			dc, _, _ := pCreateCompatibleDC.Call(0)
			bmp, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
			old, _, _ := pSelectObject.Call(dc, bmp)
			pix := unsafe.Slice((*byte)(bits), size*size*4)
			for i := 0; i < len(pix); i += 4 {
				pix[i], pix[i+1], pix[i+2], pix[i+3] = c.bg.B, c.bg.G, c.bg.R, 255
			}
			pDrawIconEx.Call(dc, 0, 0, h, uintptr(size), uintptr(size), 0, 0, 3) // DI_NORMAL
			pGdiFlush.Call()
			worst, inked := 0, 0
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					p, src := pix[(y*size+x)*4:], img.NRGBAAt(x, y)
					a := float64(src.A) / 255
					want := [3]float64{
						float64(src.R)*a + float64(c.bg.R)*(1-a),
						float64(src.G)*a + float64(c.bg.G)*(1-a),
						float64(src.B)*a + float64(c.bg.B)*(1-a),
					}
					for i, got := range [3]byte{p[2], p[1], p[0]} {
						d := int(got) - int(want[i]+0.5)
						if d < 0 {
							d = -d
						}
						worst = max(worst, d)
					}
					if src.A > 0 {
						inked++
					}
				}
			}
			pSelectObject.Call(dc, old)
			pDeleteObject.Call(bmp)
			pDeleteDC.Call(dc)
			pDestroyIcon.Call(h)
			if inked == 0 {
				t.Errorf("%s, %d px: the picture is empty", c.name, size)
			}
			if worst > 3 {
				t.Errorf("%s, %d px: Windows draws it up to %d levels away from the picture over the taskbar", c.name, size, worst)
			}
		}
	}
}
