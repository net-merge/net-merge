package winmenu

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	shcore = windows.NewLazySystemDLL("shcore.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")
	kernel = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx        = user32.NewProc("RegisterClassExW")
	pCreateWindowEx         = user32.NewProc("CreateWindowExW")
	pDestroyWindow          = user32.NewProc("DestroyWindow")
	pDefWindowProc          = user32.NewProc("DefWindowProcW")
	pGetMessage             = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessage        = user32.NewProc("DispatchMessageW")
	pPostMessage            = user32.NewProc("PostMessageW")
	pShowWindow             = user32.NewProc("ShowWindow")
	pSetWindowPos           = user32.NewProc("SetWindowPos")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pMonitorFromPoint       = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfo         = user32.NewProc("GetMonitorInfoW")
	pSetThreadDpiContext    = user32.NewProc("SetThreadDpiAwarenessContext")
	pBeginPaint             = user32.NewProc("BeginPaint")
	pEndPaint               = user32.NewProc("EndPaint")
	pInvalidateRect         = user32.NewProc("InvalidateRect")
	pTrackMouseEvent        = user32.NewProc("TrackMouseEvent")
	pSetTimer               = user32.NewProc("SetTimer")
	pKillTimer              = user32.NewProc("KillTimer")
	pGetAsyncKeyState       = user32.NewProc("GetAsyncKeyState")
	pLoadCursor             = user32.NewProc("LoadCursorW")
	pWindowFromPoint        = user32.NewProc("WindowFromPoint")
	pGetAncestor            = user32.NewProc("GetAncestor")
	pGetWindowLongPtr       = user32.NewProc("GetWindowLongPtrW")
	pGetWindowRect          = user32.NewProc("GetWindowRect")
	pSetWindowCompositionAt = user32.NewProc("SetWindowCompositionAttribute")
	pSetLayeredAttributes   = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowLongPtr       = user32.NewProc("SetWindowLongPtrW")
	pUpdateWindow           = user32.NewProc("UpdateWindow")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pGdiFlush           = gdi32.NewProc("GdiFlush")

	pGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
	pDwmSetAttr       = dwmapi.NewProc("DwmSetWindowAttribute")
	pDwmExtendFrame   = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	pGetModuleHandle  = kernel.NewProc("GetModuleHandleW")
)

const (
	wmDestroy      = 0x0002
	wmActivate     = 0x0006
	wmPaint        = 0x000F
	wmEraseBkgnd   = 0x0014
	wmNCCalcSize   = 0x0083
	wmNCHitTest    = 0x0084
	wmNCPaint      = 0x0085
	wmNCActivate   = 0x0086
	wmKeyDown      = 0x0100
	wmTimer        = 0x0113
	wmMouseMove    = 0x0200
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmMouseActive  = 0x0021
	wmMouseLeave   = 0x02A3
	wmDisplayChg   = 0x007E
	wmDPIChanged   = 0x02E0
	wmAppShow      = 0x8001
	wmAppDirty     = 0x8002
	wsPopup        = 0x80000000
	wsThickFrame   = 0x00040000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
	swShowNA       = 8
	swShow         = 5

	gwlExStyle = ^uintptr(19) // GWL_EXSTYLE, -20
	gaRoot     = 2

	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpNoCopyBits   = 0x0100 // the old pixels are not moved: every size is painted whole
	maNoActivate    = 3
	htClient        = 1
	tmeLeave        = 2

	vkEscape, vkReturn, vkSpace = 0x1B, 0x0D, 0x20
	vkEnd, vkHome               = 0x23, 0x24
	vkLeft, vkUp, vkRight, vkDn = 0x25, 0x26, 0x27, 0x28

	timerSub, timerOutside, timerReveal = 1, 2, 3
	timerFade                           = 4
	timerOpen                           = 7 // a window fading in
	timerClosing                        = 8 // on the control window: windows fading out
	subDelayMs                          = 200

	// Every menu window fades in when it appears and out when it goes. A layered window's constant
	// alpha is applied by DWM over the whole window, backdrop included: measured, at 255 it gives
	// exactly the pixels of a plain window, blur and all.
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020 // clicks pass through a window that is fading out
	lwaAlpha        = 2
	openFadeMs      = 150.0
	closeFadeMs     = 100.0
	outsideMs       = 50
	fadeTickMs      = 10
	revealTickMs    = 10
	revealSettleMs  = 30  // how long DWM gets with the finished window before it shows
	revealGiveUpMs  = 150 // the longest the main menu waits for the foreground

	dwmaCloak                              = 13
	dwmaDarkMode, dwmaCorner, dwmaBackdrop = 20, 33, 38
	cornerRound, backdropNone              = 2, 1
	backdropTransient                      = 3 // DWM's acrylic
	accentBlur, accentAcrylic              = 3, 4

	hwndTopmost = ^uintptr(0)     // HWND_TOPMOST, -1
	hwndMessage = ^uintptr(0) - 2 // HWND_MESSAGE, -3
	dpiPMv2     = ^uintptr(0) - 3 // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2, -4

	backdropBuild  = 22621 // the first build with DWMWA_SYSTEMBACKDROP_TYPE
	toggleWindowMs = 400   // a click on the icon that closed the menu must not reopen it
	overlayMaxMs   = 30000 // the longest the menu stays open under a screen capture overlay
)

const glyphCheck, glyphChevron = "", ""

type wndClassEx struct {
	size, style                 uint32
	proc                        uintptr
	clsExtra, wndExtra          int32
	inst, icon, cursor, bg      uintptr
	menuName, className, iconSm uintptr
}
type point struct{ x, y int32 }
type rect32 struct{ left, top, right, bottom int32 }
type msg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	pt             point
	_              uint32
}
type paintStruct struct {
	hdc      uintptr
	erase    int32
	rc       rect32
	restore  int32
	incUpd   int32
	reserved [32]byte
}
type monitorInfo struct {
	size    uint32
	monitor rect32
	work    rect32
	flags   uint32
}
type bitmapInfoHeader struct {
	size                   uint32
	width, height          int32
	planes, bitCount       uint16
	compression, sizeImage uint32
	xPels, yPels           int32
	clrUsed, clrImportant  uint32
}
type margins struct{ left, right, top, bottom int32 }
type trackMouse struct {
	size, flags uint32
	hwnd        uintptr
	hover       uint32
}
type accentPolicy struct{ state, flags, gradient, animation uint32 }
type compositionData struct {
	attr uint32
	data unsafe.Pointer
	size uintptr
}

// dib is a drawing surface whose pixels Go can read and write.
type dib struct {
	canvas
	dc, bmp, old uintptr
}

func newDIB(w, h int) *dib {
	bi := bitmapInfoHeader{width: int32(w), height: -int32(h), planes: 1, bitCount: 32}
	bi.size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return nil
	}
	dc, _, _ := pCreateCompatibleDC.Call(0)
	old, _, _ := pSelectObject.Call(dc, bmp)
	return &dib{canvas: canvas{pix: unsafe.Slice((*byte)(bits), w*h*4), w: w, h: h}, dc: dc, bmp: bmp, old: old}
}

func (d *dib) free() {
	if d != nil {
		pSelectObject.Call(d.dc, d.old)
		pDeleteObject.Call(d.bmp)
		pDeleteDC.Call(d.dc)
	}
}

// hasSystemBackdrop reports whether DWM's own acrylic is available. Without it the older blur is used,
// and that is tinted when it is set up, so it gets no base layer.
func hasSystemBackdrop() bool { return windows.RtlGetVersion().BuildNumber >= backdropBuild }

// render draws a whole menu over the acrylic.
func render(frame *dib, rows []row, dpi int, dark bool, bg []byte) {
	pal := paletteFor(dark)
	if !hasSystemBackdrop() {
		pal.base, pal.level = color{}, 0
	}
	if bg != nil {
		copy(frame.pix, bg)
	} else if pal.level != 0 {
		frame.fillLevel(pal.level, pal.base.a)
	} else {
		frame.fill(pal.base)
	}
	padX := px(dipPadX, dpi)
	left, right := padX, frame.w-padX
	for _, r := range rows { // shapes first: Direct2D reads the pixels when the text pass begins
		if r.sep {
			t := hairline(dpi)
			y := r.y + (r.h-t)/2
			frame.fillRoundRect(rect{0, y, frame.w, y + t}, 0, pal.sep)
		} else if r.glow > 0 && !r.disabled {
			hover := pal.hover
			hover.a *= r.glow
			frame.fillRoundRect(rect{left, r.y, right, r.y + r.h}, float64(dipRowRadius)*float64(dpi)/baseDPI, hover)
		}
	}
	initText()
	fs := formatsFor(dpi)
	pass := beginText(frame)
	for _, r := range rows {
		if r.sep {
			continue
		}
		ink := pal.text
		if r.disabled {
			ink = pal.muted
		}
		iconL := left + px(dipIconX, dpi)
		iconBox := rect{iconL, r.y, iconL + px(dipIconBox, dpi), r.y + r.h}
		if r.icon != "" {
			pass.draw(r.icon, fs.rowIcon, iconBox, ink)
		} else if r.checked {
			pass.draw(glyphCheck, fs.icon, iconBox, ink)
		}
		textRight := right - px(dipTextRight, dpi)
		trailR := right - px(dipChevronR, dpi)
		trailL := trailR - px(dipChevron, dpi)
		trail := rect{trailL, r.y, trailR, r.y + r.h}
		if r.hasSub {
			pass.draw(glyphChevron, fs.icon, trail, ink)
		} else if r.icon != "" && r.checked {
			pass.draw(glyphCheck, fs.icon, trail, ink)
		}
		if r.hasSub || (r.checkable && r.icon != "") {
			textRight = trailL - px(dipSubGap, dpi)
		}
		pass.draw(r.title, fs.text, rect{left + px(dipTextX, dpi), r.y, textRight, r.y + r.h}, ink)
	}
	pass.end()
	pGdiFlush.Call() // so the pixels are final before anything reads them
}

// level is one open window: the menu itself, or a submenu.
type level struct {
	hwnd, owner uintptr
	items       []*Item
	rows        []row
	hot         int
	x, y, w, h  int
	frame       *dib
	subFor      *Item // the row whose submenu is open
	tracking    bool
	cursor      point // where the pointer last was, to tell real moves from synthetic ones
	rowTopInPar int
	shownAt     time.Time
	activeAt    time.Time // when it was first seen with the foreground; zero until then
	glow        map[*Item]float64
	fadedAt     time.Time // when the highlights last moved; zero when none are fading
	back        *backdrop // the menu's own acrylic, when it has one
	backPix     []byte    // back, painted at the menu's current position and size
	subArmed    bool      // timerSub is running: the submenu will be matched to the pointer
	openAt      time.Time // when its fade-in began; zero while it is still cloaked
}

// setAlpha sets a layered window's opacity, from 0 to 1.
func setAlpha(h uintptr, a float64) {
	pSetLayeredAttributes.Call(h, 0, uintptr(math.Round(math.Min(math.Max(a, 0), 1)*255)), lwaAlpha)
}

// alpha is how far a window's fade-in has got.
func (l *level) alpha() float64 {
	if l.openAt.IsZero() {
		return 0
	}
	return easeOut(float64(time.Since(l.openAt).Microseconds()) / 1000 / openFadeMs)
}

// startFadeIn shows a window that was cloaked, fading it in from nothing.
func startFadeIn(h uintptr, at *time.Time) {
	*at = time.Now()
	setAlpha(h, 0)
	pSetTimer.Call(h, timerOpen, fadeTickMs, 0)
}

// closingWindow is a window fading out after it was closed. It is no longer part of the menu, and
// clicks pass through it.
type closingWindow struct {
	hwnd uintptr
	from float64 // its opacity when it began fading out
	at   time.Time
	free func()
}

var closing []closingWindow // only the menu thread touches it

// fadeOut closes a window by fading it out, then destroys it and calls free.
func fadeOut(h uintptr, from float64, free func()) {
	for _, id := range []uintptr{timerSub, timerOutside, timerReveal, timerFade, timerOpen} {
		pKillTimer.Call(h, id)
	}
	if from <= 0 {
		pDestroyWindow.Call(h)
		free()
		return
	}
	ex, _, _ := pGetWindowLongPtr.Call(h, gwlExStyle)
	pSetWindowLongPtr.Call(h, gwlExStyle, ex|wsExTransparent)
	closing = append(closing, closingWindow{hwnd: h, from: from, at: time.Now(), free: free})
	pSetTimer.Call(ctlHwnd.Load(), timerClosing, fadeTickMs, 0)
}

// closingTick fades the closing windows on, and destroys those that have gone.
func closingTick() {
	left := closing[:0]
	for _, c := range closing {
		t := float64(time.Since(c.at).Microseconds()) / 1000 / closeFadeMs
		if t >= 1 {
			pDestroyWindow.Call(c.hwnd)
			c.free()
			continue
		}
		setAlpha(c.hwnd, c.from*(1-easeIn(t)))
		left = append(left, c)
	}
	closing = left
	if len(closing) == 0 {
		pKillTimer.Call(ctlHwnd.Load(), timerClosing)
	}
}

// finishClosing destroys the closing windows at once: a dark menu is about to take a picture of the
// screen behind it, which must not have them in it.
func finishClosing() {
	for _, c := range closing {
		pDestroyWindow.Call(c.hwnd)
		c.free()
	}
	closing = nil
	pKillTimer.Call(ctlHwnd.Load(), timerClosing)
}

// easeIn is the curve of a fade-out: slow at first, then away, as WinUI's exits.
func easeIn(t float64) float64 {
	t = math.Min(math.Max(t, 0), 1)
	return t * t
}

func (l *level) bounds() rect { return rect{l.x, l.y, l.x + l.w, l.y + l.h} }

// State below is touched only on the menu thread.
var (
	levels       []*level
	ctlHwnd      atomic.Uintptr
	className    *uint16
	hInst        uintptr
	menuDPI      int
	menuWork     rect      // the work area less the gap kept from the taskbar and the screen's edges
	menuSide     int       // the edge the taskbar is on, which the menu grows away from
	overlaySince time.Time // when a screen capture overlay took the foreground; zero when none has
	menuMonitor  rect
	menuDark     bool
	lastDismiss  time.Time

	dirtyQueued atomic.Bool
	startOnce   sync.Once
	wndProcPtr  uintptr
)

// Start creates the menu's thread. It is safe to call more than once.
func Start() {
	startOnce.Do(func() {
		ready := make(chan struct{})
		go loop(ready)
		<-ready
		changed = func() {
			if !dirtyQueued.Swap(true) {
				pPostMessage.Call(ctlHwnd.Load(), wmAppDirty, 0, 0)
			}
		}
	})
}

// Popup opens the menu at the cursor, or closes it if it is open. Any goroutine can call it.
func Popup() { pPostMessage.Call(ctlHwnd.Load(), wmAppShow, 0, 0) }

func loop(ready chan struct{}) {
	runtime.LockOSThread() // windows belong to the thread that made them
	// Set per thread so it works whatever the exe's manifest says, and the rest of the process is left
	// alone. It is what keeps the menu sharp and correctly sized on any monitor, and across monitors.
	pSetThreadDpiContext.Call(dpiPMv2)
	hInst, _, _ = pGetModuleHandle.Call(0)
	className, _ = windows.UTF16PtrFromString("NetMergeFluentMenu")
	wndProcPtr = windows.NewCallback(wndProc)
	arrow, _, _ := pLoadCursor.Call(0, 32512)
	wc := wndClassEx{proc: wndProcPtr, inst: hInst, cursor: arrow, className: uintptr(unsafe.Pointer(className))}
	wc.size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	h, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, hInst, 0)
	ctlHwnd.Store(h)
	close(ready)
	var m msg
	for {
		if r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func isDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

// applyBackdrop gives a window the system's acrylic, its rounded corners and its shadow.
func applyBackdrop(h uintptr, dark, opaque bool) {
	setAttr := func(attr uintptr, v int32) {
		pDwmSetAttr.Call(h, attr, uintptr(unsafe.Pointer(&v)), 4)
	}
	if dark {
		setAttr(dwmaDarkMode, 1)
	}
	setAttr(dwmaCorner, cornerRound) // refused below Windows 11, where corners stay square
	setAccent := func(state, tint uint32) {
		accent := accentPolicy{state: state, gradient: tint}
		data := compositionData{attr: 19, data: unsafe.Pointer(&accent), size: unsafe.Sizeof(accent)}
		pSetWindowCompositionAt.Call(h, uintptr(unsafe.Pointer(&data)))
	}
	if opaque { // the menu draws its own acrylic
		setAttr(dwmaBackdrop, backdropNone)
		return
	}
	if hasSystemBackdrop() && !dark {
		// DWM's light acrylic is too dark and see-through to match the desktop's menu, a layer on top
		// can't brighten it enough, and it turns solid on an inactive window, as a submenu always is.
		// So: a plain blur, undocumented but unaffected by focus, under the palette's base layer. It
		// needs the backdrop off and no extended frame, either of which draws an opaque frame over it.
		setAttr(dwmaBackdrop, backdropNone)
		setAccent(accentBlur, 0)
		return
	}
	m := margins{-1, -1, -1, -1} // the whole client area composites over the backdrop
	pDwmExtendFrame.Call(h, uintptr(unsafe.Pointer(&m)))
	if hasSystemBackdrop() {
		setAttr(dwmaBackdrop, backdropTransient)
		return
	}
	// Windows 10 and the first Windows 11: the older, undocumented acrylic blur behind the window.
	tint := uint32(0xCCF3F3F3)
	if dark {
		tint = 0xCC2C2C2C
	}
	setAccent(accentAcrylic, tint)
}

// captureBackdrop works out a dark menu's background from the screen where it is about to appear. It is
// a snapshot, as the menu can't see behind itself once it is showing. If it fails, the menu falls back on
// DWM's acrylic under a dark layer.
func captureBackdrop(l *level) {
	if !menuDark || !hasSystemBackdrop() {
		return
	}
	sigma := float64(px(darkBlurDip, menuDPI))
	m := int(sigma) * backdropMargin
	x0, y0 := max(l.x-m, menuMonitor.left), max(l.y-m, menuMonitor.top)
	x1, y1 := min(l.x+l.w+m, menuMonitor.right), min(l.y+l.h+m, menuMonitor.bottom)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	shot := newDIB(x1-x0, y1-y0)
	if shot == nil {
		return
	}
	defer shot.free()
	const srcCopy = 0x00CC0020
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return
	}
	defer pReleaseDC.Call(0, screen)
	if r, _, _ := pBitBlt.Call(shot.dc, 0, 0, uintptr(shot.w), uintptr(shot.h), screen, uintptr(x0), uintptr(y0), srcCopy); r == 0 {
		return
	}
	pGdiFlush.Call()
	l.back = newBackdrop(shot.canvas, x0, y0, sigma)
}

func createWindow(l *level, exStyle uintptr) {
	captureBackdrop(l)
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&l.cursor)))
	l.hwnd, _, _ = pCreateWindowEx.Call(wsExTopmost|wsExToolWindow|wsExLayered|exStyle, uintptr(unsafe.Pointer(className)), 0,
		wsPopup|wsThickFrame, uintptr(int32(l.x)), uintptr(int32(l.y)), uintptr(l.w), uintptr(l.h), l.owner, 0, hInst, 0)
	if l.hwnd == 0 {
		return
	}
	setAlpha(l.hwnd, 0) // it fades in once it is uncloaked
	applyBackdrop(l.hwnd, menuDark, l.back != nil)
	l.resizeSurfaces()
	moveWindow(l, swpFrameChanged)
}

func moveWindow(l *level, flags uintptr) {
	pSetWindowPos.Call(l.hwnd, hwndTopmost, uintptr(int32(l.x)), uintptr(int32(l.y)), uintptr(l.w), uintptr(l.h), swpNoActivate|flags)
}

func (l *level) resizeSurfaces() {
	if l.frame != nil && l.frame.w == l.w && l.frame.h == l.h {
		return
	}
	l.frame.free()
	l.frame = newDIB(l.w, l.h)
	if l.back != nil {
		l.backPix = make([]byte, l.w*l.h*4)
		l.back.paint(l.backPix, l.x, l.y, l.w, l.h)
	}
}

func openRoot() {
	finishClosing()
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(pt.x))|uintptr(uint32(pt.y))<<32, 2)
	mi := monitorInfo{}
	mi.size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	work := rect{int(mi.work.left), int(mi.work.top), int(mi.work.right), int(mi.work.bottom)}
	menuMonitor = rect{int(mi.monitor.left), int(mi.monitor.top), int(mi.monitor.right), int(mi.monitor.bottom)}
	var dx, dy uint32
	menuDPI = baseDPI
	if r, _, _ := pGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		menuDPI = int(dx)
	}
	menuDark = isDark()

	mu.Lock()
	items := root
	mu.Unlock()
	l := &level{items: items, hot: -1}
	l.rows = snapshot(l.items)
	gap := px(dipEdgeGap, menuDPI)
	l.w, l.h = layoutRows(l.rows, menuDPI, 0, work.right-work.left-2*gap, textWidthAt(menuDPI))
	// menuWork is the work area less the gap: submenus and a growing menu stay inside it too.
	l.x, l.y, menuWork = placeRoot(int(pt.x), int(pt.y), l.w, l.h, work, menuMonitor, gap)
	menuSide = taskbarSide(int(pt.x), int(pt.y), work, menuMonitor)
	createWindow(l, 0)
	if l.hwnd == 0 {
		return
	}
	levels = []*level{l}
	// Shown as it is, the menu would flash: a white frame before its first paint, then a 220 ms
	// crossfade from the bright inactive backdrop to acrylic. Cloaked, it does both out of sight.
	// The activation can land after the paint, so the menu stays cloaked until it has.
	setCloaked(l.hwnd, true)
	pShowWindow.Call(l.hwnd, swShow)
	pSetForegroundWindow.Call(l.hwnd)
	pUpdateWindow.Call(l.hwnd)
	startReveal(l, true)
	pSetTimer.Call(l.hwnd, timerOutside, outsideMs, 0)
}

// startReveal uncloaks a level from its timer once it is ready. The message loop keeps running in the
// meantime, because that is what lets Windows finish activating the window.
func startReveal(l *level, needsFocus bool) {
	l.shownAt = time.Now()
	if !needsFocus {
		l.activeAt = l.shownAt
	}
	pSetTimer.Call(l.hwnd, timerReveal, revealTickMs, 0)
}

// revealTick uncloaks a level when DWM has had time to see it painted and, for the main menu, active:
// an inactive window has a bright solid backdrop that takes 220 ms to fade into acrylic. Windows may
// refuse the menu the foreground, so it gives up waiting rather than never showing it.
func revealTick(l *level) {
	now := time.Now()
	if l.activeAt.IsZero() {
		if fg, _, _ := pGetForegroundWindow.Call(); fg == l.hwnd {
			l.activeAt = now
		}
	}
	settled := !l.activeAt.IsZero() && now.Sub(l.activeAt) >= revealSettleMs*time.Millisecond
	if settled || now.Sub(l.shownAt) >= revealGiveUpMs*time.Millisecond {
		pKillTimer.Call(l.hwnd, timerReveal)
		setCloaked(l.hwnd, false)
		startFadeIn(l.hwnd, &l.openAt)
	}
}

// setCloaked hides a window from the screen while Windows still treats it as shown, so it can be
// activated and painted before anyone sees it.
func setCloaked(h uintptr, on bool) {
	v := int32(0)
	if on {
		v = 1
	}
	pDwmSetAttr.Call(h, dwmaCloak, uintptr(unsafe.Pointer(&v)), 4)
}

func openSub(parent *level, rowIdx int) {
	if menuDark && hasSystemBackdrop() {
		finishClosing() // the new submenu's backdrop is a picture of the screen, without them
	}
	r := parent.rows[rowIdx]
	mu.Lock()
	kids := r.item.kids
	mu.Unlock()
	l := &level{items: kids, hot: -1, owner: parent.hwnd, rowTopInPar: r.y}
	l.rows = snapshot(l.items)
	l.w, l.h = layoutRows(l.rows, menuDPI, 0, menuWork.right-menuWork.left, textWidthAt(menuDPI))
	l.x, l.y = placeSub(parent.bounds(), r.y, l.w, l.h, menuDPI, menuWork)
	createWindow(l, wsExNoActivate)
	if l.hwnd == 0 {
		return
	}
	parent.subFor = r.item
	levels = append(levels, l)
	setCloaked(l.hwnd, true)
	pShowWindow.Call(l.hwnd, swShowNA)
	pUpdateWindow.Call(l.hwnd)
	startReveal(l, false)
}

// closeAbove closes every level deeper than depth.
func closeAbove(depth int) {
	for len(levels) > depth+1 {
		l := levels[len(levels)-1]
		levels = levels[:len(levels)-1]
		destroy(l)
	}
	if depth >= 0 && depth < len(levels) {
		levels[depth].subFor = nil
	}
}

// destroy closes a level's window, fading it out from wherever its fade-in had got to.
func destroy(l *level) {
	frame := l.frame
	l.frame = nil
	fadeOut(l.hwnd, l.alpha(), func() { frame.free() })
}

// dismiss closes the whole menu. levels is emptied first, because destroying the active window sends
// WM_ACTIVATE straight back here.
func dismiss() {
	if len(levels) == 0 {
		return
	}
	lastDismiss = time.Now()
	overlaySince = time.Time{}
	open := levels
	levels = nil
	for i := len(open) - 1; i >= 0; i-- {
		destroy(open[i])
	}
}

func levelOf(h uintptr) (int, *level) {
	for i, l := range levels {
		if l.hwnd == h {
			return i, l
		}
	}
	return -1, nil
}

func invalidate(l *level) { pInvalidateRect.Call(l.hwnd, 0, 0) }

func setHot(l *level, i int) {
	if l.hot != i {
		l.hot = i
		startFade(l)
	}
}

// startFade sets the rows' highlights moving towards the current hot row.
func startFade(l *level) {
	if l.fadedAt.IsZero() {
		l.fadedAt = time.Now()
		pSetTimer.Call(l.hwnd, timerFade, fadeTickMs, 0)
	}
}

// fadeTick moves every highlight on by the time that has passed, and stops once they have all arrived.
func fadeTick(l *level) {
	now := time.Now()
	dt := float64(now.Sub(l.fadedAt).Microseconds()) / 1000
	l.fadedAt = now
	if l.glow == nil {
		l.glow = map[*Item]float64{}
	}
	moving := false
	for i, r := range l.rows {
		if r.item == nil {
			continue
		}
		target := 0.0
		if i == l.hot {
			target = 1
		}
		g := stepGlow(l.glow[r.item], target, dt)
		l.glow[r.item] = g
		moving = moving || g != target
	}
	if !moving {
		pKillTimer.Call(l.hwnd, timerFade)
		l.fadedAt = time.Time{}
	}
	invalidate(l)
}

// refresh shows changes to the items in the open menus. A menu grows to fit its live numbers but does
// not shrink while open, so the rows stay under the pointer.
func refresh() {
	for depth, l := range levels {
		relayout(depth, l)
		startFade(l) // the hot row may have gone
		invalidate(l)
	}
}

// relayout takes a level's rows afresh and fits its window to them.
func relayout(depth int, l *level) {
	var hotItem *Item
	if l.hot >= 0 && l.hot < len(l.rows) {
		hotItem = l.rows[l.hot].item
	}
	rows := snapshot(l.items)
	wasBottom := l.y + l.h
	w, h := layoutRows(rows, menuDPI, l.w, menuWork.right-menuWork.left, textWidthAt(menuDPI))
	l.rows, l.hot = rows, -1
	for i, r := range rows {
		if hotItem != nil && r.item == hotItem {
			l.hot = i
		}
	}
	if w == l.w && h == l.h {
		return
	}
	l.w, l.h = w, h
	var x, y int
	if depth == 0 && menuSide != sideTop {
		x, y = clampInto(l.x, wasBottom-h, w, h, menuWork) // keep its edge at the taskbar
	} else {
		x, y = clampInto(l.x, l.y, w, h, menuWork)
	}
	l.x, l.y = x, y
	l.resizeSurfaces()
	moveWindow(l, swpNoCopyBits)
}

// wantSub is the submenu the pointer asks for: the hot row's, if it has one.
func wantSub(l *level) *Item {
	if l.hot < 0 || l.hot >= len(l.rows) || !l.rows[l.hot].hasSub {
		return nil
	}
	return l.rows[l.hot].item
}

// rowOf is the index of the row showing it, or -1.
func rowOf(l *level, it *Item) int {
	if it == nil {
		return -1
	}
	for i, r := range l.rows {
		if r.item == it {
			return i
		}
	}
	return -1
}

// cursorInDeeper reports whether the pointer is over a submenu opened from this level, or from one of
// its submenus.
func cursorInDeeper(depth int) bool {
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	for _, l := range levels[min(depth+1, len(levels)):] {
		if l.bounds().contains(int(pt.x), int(pt.y)) {
			return true
		}
	}
	return false
}

func rowAt(l *level, y int) int {
	for i, r := range l.rows {
		if !r.sep && y >= r.y && y < r.y+r.h {
			return i
		}
	}
	return -1
}

// activate is a click or Enter on a row.
func activate(depth int, l *level, i int) {
	if i < 0 || i >= len(l.rows) || l.rows[i].disabled || l.rows[i].sep {
		return
	}
	r := l.rows[i]
	if r.hasSub {
		if l.subFor != r.item {
			closeAbove(depth)
			openSub(l, i)
		}
		return
	}
	dismiss()
	r.item.click()
}

// nextRow is the next row a keyboard can land on, wrapping around.
func nextRow(l *level, from, step int) int {
	n := len(l.rows)
	if n == 0 {
		return -1
	}
	i := from
	if i < 0 && step < 0 {
		i = 0
	}
	for range n {
		i = (i + step + n) % n
		if !l.rows[i].sep && !l.rows[i].disabled {
			return i
		}
	}
	return -1
}

func onKey(vk uintptr) {
	if len(levels) == 0 {
		return
	}
	depth := len(levels) - 1
	l := levels[depth]
	switch vk {
	case vkDn:
		setHot(l, nextRow(l, l.hot, 1))
	case vkUp:
		setHot(l, nextRow(l, l.hot, -1))
	case vkHome:
		setHot(l, nextRow(l, -1, 1))
	case vkEnd:
		setHot(l, nextRow(l, 0, -1))
	case vkRight:
		if l.hot >= 0 && l.rows[l.hot].hasSub {
			activate(depth, l, l.hot)
			sub := levels[len(levels)-1]
			setHot(sub, nextRow(sub, -1, 1))
		}
	case vkReturn, vkSpace:
		if l.hot >= 0 {
			wasSub := l.rows[l.hot].hasSub
			activate(depth, l, l.hot)
			if wasSub {
				sub := levels[len(levels)-1]
				setHot(sub, nextRow(sub, -1, 1))
			}
		}
	case vkLeft:
		if depth > 0 {
			closeAbove(depth - 1)
		}
	case vkEscape:
		if depth > 0 {
			closeAbove(depth - 1)
		} else {
			dismiss()
		}
	}
}

// clickedOutside covers what WM_ACTIVATE can't: Windows may refuse the menu the foreground, and then
// it is never told when the user clicks elsewhere.
func clickedOutside() bool {
	const down = 0x8000
	any := false
	for _, vk := range []uintptr{1, 2, 4} { // left, right, middle
		s, _, _ := pGetAsyncKeyState.Call(vk)
		any = any || s&down != 0
	}
	if !any {
		return false
	}
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	for _, l := range levels {
		if l.bounds().contains(int(pt.x), int(pt.y)) {
			return false
		}
	}
	// Dragging out a screenshot over the menu is a click outside it, but must not close it.
	under, _, _ := pWindowFromPoint.Call(uintptr(uint32(pt.x)) | uintptr(uint32(pt.y))<<32)
	root, _, _ := pGetAncestor.Call(under, gaRoot)
	return !isCaptureOverlay(root)
}

// lostFocus reports whether another window has taken the foreground from the menu, which closes it as
// it would close the desktop's menu. A screen capture overlay doesn't count: it comes up over the menu
// to take a picture of it. Nor does a menu that Windows never gave the foreground in the first place.
func lostFocus() bool {
	if len(levels) == 0 {
		return false
	}
	fg, _, _ := pGetForegroundWindow.Call()
	if _, l := levelOf(fg); l != nil {
		if levels[0].activeAt.IsZero() { // given the foreground after its reveal stopped watching
			levels[0].activeAt = time.Now()
		}
		overlaySince = time.Time{}
		return false
	}
	if levels[0].activeAt.IsZero() {
		return false
	}
	if !isCaptureOverlay(fg) {
		return true
	}
	// A screenshot takes seconds. Anything that stays on top of the whole screen far longer, like
	// a full-screen game, isn't one, and the menu must not be left floating over it.
	if overlaySince.IsZero() {
		overlaySince = time.Now()
	}
	return time.Since(overlaySince) > overlayMaxMs*time.Millisecond
}

// isCaptureOverlay recognises the screen capture overlays of Snipping Tool (Win+Shift+S, Print Screen)
// and of tools like ShareX and Greenshot by what they have in common, rather than by name: an
// always-on-top window that covers the whole of the menu's monitor.
func isCaptureOverlay(h uintptr) bool {
	if h == 0 {
		return false
	}
	if ex, _, _ := pGetWindowLongPtr.Call(h, gwlExStyle); ex&wsExTopmost == 0 {
		return false
	}
	var r rect32
	if ok, _, _ := pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return false
	}
	return int(r.left) <= menuMonitor.left && int(r.top) <= menuMonitor.top &&
		int(r.right) >= menuMonitor.right && int(r.bottom) >= menuMonitor.bottom
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if hwnd == ctlHwnd.Load() {
		switch message {
		case wmAppShow:
			if len(levels) > 0 {
				dismiss()
			} else if time.Since(lastDismiss) >= toggleWindowMs*time.Millisecond {
				openRoot()
			}
			return 0
		case wmAppDirty:
			dirtyQueued.Store(false)
			refresh()
			return 0
		case wmTimer:
			if wParam == timerClosing {
				closingTick()
			}
			return 0
		}
		r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
		return r
	}
	depth, l := levelOf(hwnd)
	switch message {
	case wmNCCalcSize:
		if wParam != 0 {
			return 0 // no frame: the thick frame is only there for DWM's backdrop, corners and shadow
		}
	case wmNCHitTest:
		return htClient // and it must not resize
	case wmNCPaint:
		return 0 // there is no frame to draw, and drawing one would put a white ring over the menu's edge
	case wmNCActivate:
		// -1 tells DefWindowProc to record the activation without redrawing the frame, which would
		// otherwise draw over a menu that was painted before Windows finished activating it.
		r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, ^uintptr(0))
		return r
	case wmEraseBkgnd:
		return 1
	case wmMouseActive:
		if depth > 0 {
			return maNoActivate
		}
	case wmPaint:
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if l != nil && l.frame != nil {
			for i, r := range l.rows {
				l.rows[i].glow = l.glow[r.item]
			}
			render(l.frame, l.rows, menuDPI, menuDark, l.backPix)
			const srcCopy = 0x00CC0020
			pBitBlt.Call(dc, 0, 0, uintptr(l.w), uintptr(l.h), l.frame.dc, 0, 0, srcCopy)
		}
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmActivate:
		// Losing the foreground is handled on timerOutside, which can see what took it: the menu stays
		// open under a screen capture overlay, so the screenshot has it.
	case wmDPIChanged, wmDisplayChg:
		// The scale or the monitors changed under an open menu. It reopens at the right size next time.
		dismiss()
		return 0
	case wmKeyDown:
		onKey(wParam)
		return 0
	case wmMouseMove:
		if l == nil {
			break
		}
		// Windows also sends this when a window appears under a still pointer. That is not the user
		// choosing a row, and it would undo a keyboard selection and close the submenu.
		var pt point
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		if pt == l.cursor {
			return 0
		}
		l.cursor = pt
		if !l.tracking {
			tm := trackMouse{flags: tmeLeave, hwnd: hwnd}
			tm.size = uint32(unsafe.Sizeof(tm))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
			l.tracking = true
		}
		i := rowAt(l, int(int16(lParam>>16)))
		if i >= 0 && l.rows[i].disabled {
			i = -1
		}
		moved := i != l.hot
		setHot(l, i)
		// The submenu is matched to the pointer after a pause: on a new row, or whenever the open one
		// isn't the hot row's.
		if moved || (!l.subArmed && wantSub(l) != l.subFor) {
			l.subArmed = true
			pSetTimer.Call(hwnd, timerSub, subDelayMs, 0)
		}
		return 0
	case wmMouseLeave:
		if l != nil {
			l.tracking = false
			// The row with an open submenu stays lit, though the pointer may have left over another.
			setHot(l, rowOf(l, l.subFor))
		}
		return 0
	case wmLButtonUp, wmRButtonUp:
		if l != nil {
			activate(depth, l, rowAt(l, int(int16(lParam>>16))))
		}
		return 0
	case wmTimer:
		if l == nil {
			break
		}
		switch wParam {
		case timerOutside:
			if lostFocus() || clickedOutside() {
				dismiss()
			}
		case timerReveal:
			revealTick(l)
		case timerFade:
			fadeTick(l)
		case timerOpen:
			a := l.alpha()
			setAlpha(hwnd, a)
			if a >= 1 {
				pKillTimer.Call(hwnd, timerOpen)
			}
		case timerSub:
			pKillTimer.Call(hwnd, timerSub)
			l.subArmed = false
			if cursorInDeeper(depth) {
				// The pointer crossed other rows on its way into the open submenu: it stays, and its
				// row is the one lit.
				setHot(l, rowOf(l, l.subFor))
				break
			}
			if want := wantSub(l); want != l.subFor {
				closeAbove(depth)
				if want != nil {
					openSub(l, l.hot)
				}
			}
		}
		return 0
	case wmDestroy:
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}
