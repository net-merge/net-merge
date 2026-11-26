package winmenu

import (
	"math"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Text goes through DirectWrite and Direct2D, as it does in the desktop's XAML menu: exact fractional
// sizes, no snapping of the strokes to the pixel grid, and grayscale antialiasing that writes real alpha
// over the acrylic. GDI would round 17.5 px up to 18 and hint the strokes into something harder.
//
// There is no Go binding, so the COM methods are called by their index in the vtable. The tray uses this
// menu on amd64 only: DrawTextLayout's by-value point would be passed differently on arm64.

var (
	d2d1   = windows.NewLazySystemDLL("d2d1.dll")
	dwrite = windows.NewLazySystemDLL("dwrite.dll")

	pD2D1CreateFactory   = d2d1.NewProc("D2D1CreateFactory")
	pDWriteCreateFactory = dwrite.NewProc("DWriteCreateFactory")

	iidD2DFactory = windows.GUID{Data1: 0x06152247, Data2: 0x6f50, Data3: 0x465a, Data4: [8]byte{0x92, 0x45, 0x11, 0x8b, 0xfd, 0x3b, 0x60, 0x07}}
	iidDWFactory  = windows.GUID{Data1: 0xb859ee5a, Data2: 0xd838, Data3: 0x4b5b, Data4: [8]byte{0xa2, 0xe8, 0x1a, 0xdc, 0x7d, 0x93, 0xdb, 0x48}}
)

// vtable indexes, from d2d1.h and dwrite.h.
const (
	comRelease = 2

	d2dFactoryCreateDCTarget = 16

	rtCreateSolidBrush = 8
	rtDrawTextLayout   = 28
	rtSetTextAntialias = 34
	rtSetTextParams    = 36
	rtBeginDraw        = 48
	rtEndDraw          = 49
	dcBindDC           = 57
	brushSetColor      = 8

	dwGetSystemFonts   = 3
	dwCustomParams     = 12
	dwCreateTextFormat = 15
	dwCreateTextLayout = 18
	dwEllipsisSign     = 20

	collFindFamily = 5

	fmtSetTextAlign = 3
	fmtSetParaAlign = 4
	fmtSetWrapping  = 5
	fmtSetTrimming  = 9

	layoutGetMetrics = 60
)

type comObject struct{ vtable *[128]uintptr }

func (o *comObject) call(index int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtable[index], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObject) release() {
	if o != nil {
		o.call(comRelease)
	}
}

// float32Arg passes a float to a COM method: Go's syscall puts each argument in both the integer and
// the floating-point register, and on the stack only the low 32 bits are read.
func float32Arg(f float64) uintptr { return uintptr(math.Float32bits(float32(f))) }

func failed(hr uintptr) bool { return int32(hr) < 0 }

// textGamma is how the antialiased edges of the text are weighted. Measured against "Personalize" in the
// desktop's menu, 1.8 gives the same pixels (1.0 is visibly thin, 2.2 visibly heavy).
const textGamma = 1.8

// Only the menu thread touches these.
var (
	d2dFactory *comObject
	dwFactory  *comObject
	dcTarget   *comObject
	textBrush  *comObject
	formats    = map[int]textFormats{} // per DPI
)

type textFormats struct{ text, icon, rowIcon *comObject }

func initText() {
	if d2dFactory == nil {
		pD2D1CreateFactory.Call(0, uintptr(unsafe.Pointer(&iidD2DFactory)), 0, uintptr(unsafe.Pointer(&d2dFactory)))
	}
	if dwFactory == nil {
		pDWriteCreateFactory.Call(0, uintptr(unsafe.Pointer(&iidDWFactory)), uintptr(unsafe.Pointer(&dwFactory)))
	}
}

// ensureTarget makes the Direct2D target that draws into a DIB's premultiplied pixels. It uses the CPU:
// a menu is small, and a software target can't be lost with the graphics driver.
func ensureTarget() bool {
	initText()
	if d2dFactory == nil || dwFactory == nil {
		return false
	}
	if dcTarget != nil {
		return true
	}
	const software, bgra8, premultiplied, grayscale = 1, 87, 1, 2
	props := struct {
		kind, format, alpha uint32
		dpiX, dpiY          float32 // 96, so that a Direct2D unit is one pixel: sizes are already scaled
		usage, minLevel     uint32
	}{kind: software, format: bgra8, alpha: premultiplied, dpiX: baseDPI, dpiY: baseDPI}
	if failed(d2dFactory.call(d2dFactoryCreateDCTarget, uintptr(unsafe.Pointer(&props)), uintptr(unsafe.Pointer(&dcTarget)))) || dcTarget == nil {
		dcTarget = nil
		return false
	}
	dcTarget.call(rtSetTextAntialias, grayscale)
	var params *comObject
	const flatPixels, naturalSymmetric = 0, 5
	dwFactory.call(dwCustomParams, float32Arg(textGamma), float32Arg(0), float32Arg(0), flatPixels, naturalSymmetric, uintptr(unsafe.Pointer(&params)))
	if params != nil {
		dcTarget.call(rtSetTextParams, uintptr(unsafe.Pointer(params)))
		params.release()
	}
	white := [4]float32{1, 1, 1, 1}
	dcTarget.call(rtCreateSolidBrush, uintptr(unsafe.Pointer(&white)), 0, uintptr(unsafe.Pointer(&textBrush)))
	return textBrush != nil
}

// firstInstalled is the first of the named font families that this PC has.
func firstInstalled(names ...string) string {
	var fonts *comObject
	dwFactory.call(dwGetSystemFonts, uintptr(unsafe.Pointer(&fonts)), 0)
	if fonts == nil {
		return names[len(names)-1]
	}
	defer fonts.release()
	for _, name := range names {
		var index, exists uint32
		wname, _ := windows.UTF16PtrFromString(name)
		fonts.call(collFindFamily, uintptr(unsafe.Pointer(wname)), uintptr(unsafe.Pointer(&index)), uintptr(unsafe.Pointer(&exists)))
		if exists != 0 {
			return name
		}
	}
	return names[len(names)-1]
}

func newFormat(family string, sizePx float64) *comObject {
	const regular, normalStyle, normalStretch = 400, 0, 5
	var f *comObject
	wfamily, _ := windows.UTF16PtrFromString(family)
	wlocale, _ := windows.UTF16PtrFromString("en-us")
	dwFactory.call(dwCreateTextFormat, uintptr(unsafe.Pointer(wfamily)), 0, regular, normalStyle, normalStretch, float32Arg(sizePx), uintptr(unsafe.Pointer(wlocale)), uintptr(unsafe.Pointer(&f)))
	if f != nil {
		const paraCenter, noWrap = 2, 1
		f.call(fmtSetParaAlign, paraCenter)
		f.call(fmtSetWrapping, noWrap)
	}
	return f
}

func formatsFor(dpi int) textFormats {
	fs, ok := formats[dpi]
	if ok {
		return fs
	}
	scale := float64(dpi) / baseDPI
	icons := firstInstalled("Segoe Fluent Icons", "Segoe MDL2 Assets")
	fs = textFormats{
		text:    newFormat(firstInstalled("Segoe UI Variable Text", "Segoe UI"), dipFont*scale),
		icon:    newFormat(icons, dipChevron*scale),
		rowIcon: newFormat(icons, dipIconBox*scale),
	}
	if fs.text != nil { // a title too long for the monitor ends in an ellipsis
		var sign *comObject
		dwFactory.call(dwEllipsisSign, uintptr(unsafe.Pointer(fs.text)), uintptr(unsafe.Pointer(&sign)))
		const byCharacter = 1
		trimming := [3]uint32{byCharacter, 0, 0}
		fs.text.call(fmtSetTrimming, uintptr(unsafe.Pointer(&trimming)), uintptr(unsafe.Pointer(sign)))
		sign.release()
	}
	const textCenter = 2
	for _, f := range []*comObject{fs.icon, fs.rowIcon} {
		if f != nil {
			f.call(fmtSetTextAlign, textCenter)
		}
	}
	formats[dpi] = fs
	return fs
}

func newLayout(s string, f *comObject, w, h float64) *comObject {
	if f == nil || s == "" {
		return nil
	}
	u, _ := windows.UTF16FromString(s)
	var l *comObject
	dwFactory.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(f)), float32Arg(w), float32Arg(h), uintptr(unsafe.Pointer(&l)))
	return l
}

func textWidthAt(dpi int) func(string) int {
	return func(s string) int {
		initText()
		if dwFactory == nil {
			return 0
		}
		l := newLayout(s, formatsFor(dpi).text, 1e6, 1e6)
		if l == nil {
			return 0
		}
		defer l.release()
		var m struct {
			left, top, width, withTrailing, height, layoutW, layoutH float32
			depth, lines                                             uint32
		}
		l.call(layoutGetMetrics, uintptr(unsafe.Pointer(&m)))
		return int(math.Ceil(float64(m.withTrailing)))
	}
}

// textPass is one batch of text drawn into a frame, between Direct2D's BeginDraw and EndDraw.
type textPass struct{ ok bool }

func beginText(frame *dib) textPass {
	if !ensureTarget() {
		return textPass{}
	}
	rc := rect32{0, 0, int32(frame.w), int32(frame.h)}
	if failed(dcTarget.call(dcBindDC, frame.dc, uintptr(unsafe.Pointer(&rc)))) {
		return textPass{}
	}
	dcTarget.call(rtBeginDraw)
	return textPass{ok: true}
}

func (p textPass) draw(s string, f *comObject, r rect, c color) {
	if !p.ok || r.left >= r.right || r.top >= r.bottom {
		return
	}
	l := newLayout(s, f, float64(r.right-r.left), float64(r.bottom-r.top))
	if l == nil {
		return
	}
	defer l.release()
	col := [4]float32{float32(c.r) / 255, float32(c.g) / 255, float32(c.b) / 255, float32(c.a)}
	textBrush.call(brushSetColor, uintptr(unsafe.Pointer(&col)))
	origin := uintptr(math.Float32bits(float32(r.left))) | uintptr(math.Float32bits(float32(r.top)))<<32
	dcTarget.call(rtDrawTextLayout, origin, uintptr(unsafe.Pointer(l)), uintptr(unsafe.Pointer(textBrush)), 0)
}

func (p textPass) end() {
	if !p.ok {
		return
	}
	if failed(dcTarget.call(rtEndDraw, 0, 0)) { // make new ones on the next paint
		textBrush.release()
		dcTarget.release()
		textBrush, dcTarget = nil, nil
	}
}
