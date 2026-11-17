package tray

// The Windows flyout uses Win32 windows, DWM corners, system fonts and Segoe
// Fluent glyphs. No browser/runtime is embedded and network work stays off the UI
// thread. Other platforms retain their existing native tray menus.
import (
	"fmt"
	"image"
	"math"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"net-merge/internal/logx"
	"net-merge/internal/nics"
	"net-merge/internal/provider"
	"net-merge/internal/state"
)

var (
	winUser         = windows.NewLazySystemDLL("user32.dll")
	winGDI          = windows.NewLazySystemDLL("gdi32.dll")
	winDWM          = windows.NewLazySystemDLL("dwmapi.dll")
	pCreateWindow   = winUser.NewProc("CreateWindowExW")
	pDefaultWindow  = winUser.NewProc("DefWindowProcW")
	pPost           = winUser.NewProc("PostMessageW")
	pShow           = winUser.NewProc("ShowWindow")
	pPosition       = winUser.NewProc("SetWindowPos")
	pInvalidate     = winUser.NewProc("InvalidateRect")
	pForeground     = winUser.NewProc("SetForegroundWindow")
	pGetCursor      = winUser.NewProc("GetCursorPos")
	pGetDPI         = winUser.NewProc("GetDpiForWindow")
	pBeginPaint     = winUser.NewProc("BeginPaint")
	pEndPaint       = winUser.NewProc("EndPaint")
	pGetClient      = winUser.NewProc("GetClientRect")
	pFillRect       = winUser.NewProc("FillRect")
	pDrawText       = winUser.NewProc("DrawTextW")
	pCreateFont     = winGDI.NewProc("CreateFontW")
	pDeleteObject   = winGDI.NewProc("DeleteObject")
	pSelect         = winGDI.NewProc("SelectObject")
	pBrush          = winGDI.NewProc("CreateSolidBrush")
	pTextColor      = winGDI.NewProc("SetTextColor")
	pBackgroundMode = winGDI.NewProc("SetBkMode")
	pRoundRect      = winGDI.NewProc("RoundRect")
	pStockObject    = winGDI.NewProc("GetStockObject")
	pStretchDIB     = winGDI.NewProc("StretchDIBits")
	panelWindows    sync.Map // HWND -> *flyoutWindow, accessed on its owning thread
	panelWndProc    = syscall.NewCallback(flyoutProc)
)

const (
	wmPanelShow    = 0x8001
	wmPanelRefresh = 0x8002
	wmPanelClose   = 0x8003
)

type panelRect struct{ Left, Top, Right, Bottom int32 }
type panelPoint struct{ X, Y int32 }
type panelMessage struct {
	Hwnd           uintptr
	Message        uint32
	Wparam, Lparam uintptr
	Time           uint32
	Point          panelPoint
	Private        uint32
}
type panelClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type panelPaint struct {
	DC                 uintptr
	Erase              int32
	Rect               panelRect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type panelBitmap struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, ImageSize uint32
	XPels, YPels           int32
	Colors, Important      uint32
}
type panelMonitor struct {
	Size          uint32
	Monitor, Work panelRect
	Flags         uint32
}

type panelPalette struct{ background, hover, text, muted, line, accent, green, header uint32 }

type panelRow struct {
	label, detail, icon, right, kind, name string
	value                                  float64
	checked, disabled, separator           bool
	logo                                   image.Image
	height                                 int
	rect                                   panelRect
}

type panelSnapshot struct {
	nativeView
	hardware  []nics.Hardware
	providers map[string]provider.Info
	latest    string
}

type windowsPanel struct {
	mu            sync.Mutex
	model         panelSnapshot
	main, sub     *flyoutWindow
	resolver      *provider.Resolver
	clicks        chan<- nativeAction
	dir           string
	prefs         state.TrayPreferences // only accessed on the window thread
	closeOnce     sync.Once
	lastInventory time.Time
	inventory     []nics.Hardware
	preview       bool
	previewTheme  string
}

type flyoutWindow struct {
	panel               *windowsPanel
	hwnd                uintptr
	sub                 bool
	adapter             string
	visible             bool
	width, height, x, y int
	dpi                 int
	rows                []panelRow
	hover               int
	scroll              int
	palette             panelPalette
	fonts               [4]uintptr // normal, small, title, glyph
	input               uintptr
	editing             bool
	editOldProc         uintptr
}

func newWindowsPanel(dir string, clicks chan<- nativeAction, preview ...bool) (*windowsPanel, error) {
	p := &windowsPanel{dir: dir, clicks: clicks, resolver: provider.New(), prefs: state.LoadTrayPreferences(dir)}
	if len(preview) > 0 {
		p.preview = preview[0]
		p.previewTheme = os.Getenv("NET_MERGE_PREVIEW_THEME")
	}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		p.resolver.Close()
		return nil, err
	}
	return p, nil
}

func (p *windowsPanel) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	name := windows.StringToUTF16Ptr("NetMerge.FluentFlyout")
	cursor, _, _ := winUser.NewProc("LoadCursorW").Call(0, 32512)
	class := panelClass{Proc: panelWndProc, Instance: instance, Cursor: cursor, Name: name, Style: 0x0008}
	class.Size = uint32(unsafe.Sizeof(class))
	if result, _, err := winUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); result == 0 {
		ready <- fmt.Errorf("register flyout: %w", err)
		return
	}
	defer winUser.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	create := func(sub bool, owner uintptr) (*flyoutWindow, error) {
		// WS_POPUP | WS_BORDER; TOOLWINDOW avoids a second taskbar button.
		extended := uintptr(0x80)
		if p.preview && !sub {
			extended = 0x40000
		}
		hwnd, _, err := pCreateWindow.Call(extended, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("net-merge"))), 0x82800000,
			0, 0, 560, 500, owner, 0, instance, 0)
		if hwnd == 0 {
			return nil, fmt.Errorf("create flyout: %w", err)
		}
		w := &flyoutWindow{panel: p, hwnd: hwnd, sub: sub, hover: -1, dpi: 96}
		panelWindows.Store(hwnd, w)
		w.theme()
		return w, nil
	}
	var err error
	p.main, err = create(false, 0)
	if err != nil {
		ready <- err
		return
	}
	p.sub, err = create(true, p.main.hwnd)
	if err != nil {
		winUser.NewProc("DestroyWindow").Call(p.main.hwnd)
		ready <- err
		return
	}
	ready <- nil
	var msg panelMessage
	for {
		result, _, _ := winUser.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		winUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		winUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	for _, w := range []*flyoutWindow{p.sub, p.main} {
		w.freeFonts()
		panelWindows.Delete(w.hwnd)
	}
}

func (p *windowsPanel) Show() { pPost.Call(p.main.hwnd, wmPanelShow, 0, 0) }
func (p *windowsPanel) Close() {
	p.closeOnce.Do(func() { p.resolver.Close(); pPost.Call(p.main.hwnd, wmPanelClose, 0, 0) })
}
func (p *windowsPanel) SetLatest(version string) {
	p.mu.Lock()
	p.model.latest = version
	p.mu.Unlock()
	pPost.Call(p.main.hwnd, wmPanelRefresh, 0, 0)
}
func (p *windowsPanel) Refresh(view nativeView) {
	// Called by the tray's one-second ticker, not by a Win32 input callback.
	if time.Since(p.lastInventory) > 3*time.Second {
		if inventory, err := nics.HardwareAdapters(); err == nil {
			p.inventory = inventory
		}
		p.lastInventory = time.Now()
	}
	var adapters []nics.Nic
	for _, s := range view.NICs {
		if ifc, err := net.InterfaceByName(s.Name); err == nil {
			adapters = append(adapters, nics.Nic{Name: s.Name, Index: ifc.Index, IP: net.ParseIP(s.IP), IP6: net.ParseIP(s.IP6)})
		}
	}
	p.resolver.Refresh(adapters)
	providers := map[string]provider.Info{}
	for _, s := range view.NICs {
		providers[s.Name] = p.resolver.Get(s.Name)
	}
	p.mu.Lock()
	p.model.nativeView = view
	p.model.hardware = append([]nics.Hardware(nil), p.inventory...)
	p.model.providers = providers
	p.mu.Unlock()
	pPost.Call(p.main.hwnd, wmPanelRefresh, 0, 0)
}
func (p *windowsPanel) snapshot() panelSnapshot { p.mu.Lock(); defer p.mu.Unlock(); return p.model }

func (p *windowsPanel) emit(a nativeAction) {
	select {
	case p.clicks <- a:
	default:
	}
}

func flyoutProc(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	v, ok := panelWindows.Load(hwnd)
	if !ok {
		result, _, _ := pDefaultWindow.Call(hwnd, uintptr(message), wp, lp)
		return result
	}
	w := v.(*flyoutWindow)
	switch message {
	case wmPanelShow:
		if w.visible {
			w.panel.hide()
		} else {
			w.open()
		}
		return 0
	case wmPanelRefresh:
		for _, win := range []*flyoutWindow{w.panel.main, w.panel.sub} {
			if win.visible && !win.editing {
				win.rebuild()
				pInvalidate.Call(win.hwnd, 0, 0)
			}
		}
		return 0
	case wmPanelClose:
		winUser.NewProc("DestroyWindow").Call(w.panel.sub.hwnd)
		winUser.NewProc("DestroyWindow").Call(w.panel.main.hwnd)
		winUser.NewProc("PostQuitMessage").Call(0)
		return 0
	case 0x0010: // WM_CLOSE: dismiss, do not stop merging.
		w.panel.hide()
		return 0
	case 0x0006: // WM_ACTIVATE
		if !w.panel.preview && wp&0xffff == 0 && lp != w.panel.main.hwnd && lp != w.panel.sub.hwnd {
			w.panel.hide()
		}
		return 0
	case 0x0014:
		return 1 // WM_ERASEBKGND: paint one double-buffered frame.
	case 0x000f:
		w.paint()
		return 0
	case 0x0200:
		x, y := int(int16(lp&0xffff)), int(int16((lp>>16)&0xffff))
		hover := w.hit(x, y)
		if hover != w.hover {
			w.hover = hover
			pInvalidate.Call(hwnd, 0, 0)
		}
		return 0
	case 0x0202:
		if i := w.hit(int(int16(lp&0xffff)), int(int16((lp>>16)&0xffff))); i >= 0 {
			w.activate(i)
		}
		return 0
	case 0x020a: // WM_MOUSEWHEEL
		w.scroll = max(0, w.scroll-int(int16(wp>>16))/120*w.s(90))
		w.rebuild()
		pInvalidate.Call(hwnd, 0, 0)
		return 0
	case 0x0100: // WM_KEYDOWN
		switch wp {
		case 0x1b:
			if w.editing {
				w.cancelEdit()
			} else {
				w.panel.hide()
			}
		case 0x26:
			w.move(-1)
		case 0x28, 0x09:
			w.move(1)
		case 0x0d, 0x20:
			if w.hover >= 0 {
				w.activate(w.hover)
			}
		case 0x27:
			if !w.sub && w.hover >= 0 && w.rows[w.hover].kind == "adaptermenu" {
				w.activate(w.hover)
			}
		case 0x25:
			if w.sub {
				w.hide()
				pForeground.Call(w.panel.main.hwnd)
			}
		}
		return 0
	case 0x001a, 0x031a: // Theme changes follow Windows while the flyout is open.
		w.theme()
		pInvalidate.Call(hwnd, 0, 0)
		return 0
	case 0x02e0: // Per-monitor DPI change.
		w.theme()
		w.rebuild()
		pInvalidate.Call(hwnd, 0, 0)
		return 0
	}
	result, _, _ := pDefaultWindow.Call(hwnd, uintptr(message), wp, lp)
	return result
}

func (w *flyoutWindow) s(value int) int { return (value*w.dpi + 48) / 96 }
func (w *flyoutWindow) freeFonts() {
	for _, font := range w.fonts {
		if font != 0 {
			pDeleteObject.Call(font)
		}
	}
	w.fonts = [4]uintptr{}
}

func (w *flyoutWindow) theme() {
	if pGetDPI.Find() == nil {
		if dpi, _, _ := pGetDPI.Call(w.hwnd); dpi > 0 {
			w.dpi = int(dpi)
		}
	}
	dark := true
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err == nil {
		value, _, err := key.GetIntegerValue("AppsUseLightTheme")
		if err == nil {
			dark = value == 0
		}
		key.Close()
	}
	if w.panel.previewTheme == "light" {
		dark = false
	}
	if w.panel.previewTheme == "dark" {
		dark = true
	}
	if dark {
		w.palette = panelPalette{background: rgb(32, 32, 32), hover: rgb(51, 51, 51), text: rgb(245, 245, 245), muted: rgb(175, 175, 175), line: rgb(57, 57, 57), accent: rgb(117, 206, 255), green: rgb(89, 221, 145), header: rgb(37, 48, 54)}
	} else {
		w.palette = panelPalette{background: rgb(249, 249, 249), hover: rgb(235, 235, 235), text: rgb(28, 28, 28), muted: rgb(98, 98, 98), line: rgb(221, 221, 221), accent: rgb(0, 95, 160), green: rgb(16, 128, 70), header: rgb(229, 241, 249)}
	}
	var accent uint32
	var opaque int32
	if result, _, _ := winDWM.NewProc("DwmGetColorizationColor").Call(uintptr(unsafe.Pointer(&accent)), uintptr(unsafe.Pointer(&opaque))); result == 0 {
		color := rgb((accent>>16)&255, (accent>>8)&255, accent&255)
		w.palette.header = blendColor(w.palette.background, color, 0.1)
		w.palette.accent = readableAccent(color, w.palette.header, dark)
	}
	// Respect Windows High Contrast rather than forcing an app palette.
	high := struct {
		Size, Flags uint32
		Scheme      *uint16
	}{}
	high.Size = uint32(unsafe.Sizeof(high))
	winUser.NewProc("SystemParametersInfoW").Call(0x0042, uintptr(high.Size), uintptr(unsafe.Pointer(&high)), 0)
	if high.Flags&1 != 0 {
		bg, _, _ := winUser.NewProc("GetSysColor").Call(5)
		fg, _, _ := winUser.NewProc("GetSysColor").Call(8)
		hi, _, _ := winUser.NewProc("GetSysColor").Call(13)
		w.palette = panelPalette{background: uint32(bg), text: uint32(fg), muted: uint32(fg), line: uint32(fg), accent: uint32(fg), green: uint32(fg), hover: uint32(hi), header: uint32(bg)}
	}
	darkValue := int32(0)
	if dark {
		darkValue = 1
	}
	corner := int32(2)
	attr := winDWM.NewProc("DwmSetWindowAttribute")
	attr.Call(w.hwnd, 20, uintptr(unsafe.Pointer(&darkValue)), 4)
	attr.Call(w.hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	attr.Call(w.hwnd, 34, uintptr(unsafe.Pointer(&w.palette.line)), 4)
	w.freeFonts()
	for i, size := range []int{16, 12, 18, 20} {
		face := "Segoe UI"
		if i == 3 {
			face = "Segoe Fluent Icons"
			if windows.RtlGetVersion().BuildNumber < 22000 {
				face = "Segoe MDL2 Assets"
			}
		}
		weight := 400
		if i == 2 {
			weight = 600
		}
		font, _, _ := pCreateFont.Call(uintptr(int32(-w.s(size))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(face))))
		w.fonts[i] = font
	}
}

func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

func blendColor(a, b uint32, amount float64) uint32 {
	var out uint32
	for _, shift := range []uint{0, 8, 16} {
		v := float64((a>>shift)&255)*(1-amount) + float64((b>>shift)&255)*amount
		out |= uint32(v) << shift
	}
	return out
}

func luminance(color uint32) float64 {
	values := [3]float64{}
	for i, shift := range []uint{0, 8, 16} {
		v := float64((color>>shift)&255) / 255
		if v <= 0.04045 {
			values[i] = v / 12.92
		} else {
			values[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return values[0]*0.2126 + values[1]*0.7152 + values[2]*0.0722
}
func readableAccent(color, background uint32, dark bool) uint32 {
	target := uint32(0)
	if dark {
		target = rgb(255, 255, 255)
	}
	for step := 0; step <= 10; step++ {
		candidate := blendColor(color, target, float64(step)/10)
		a, b := luminance(candidate), luminance(background)
		if (max(a, b)+0.05)/(min(a, b)+0.05) >= 4.5 {
			return candidate
		}
	}
	return target
}

func (w *flyoutWindow) open() {
	w.theme()
	w.scroll = 0
	w.hover = -1
	w.rebuild()
	var cursor panelPoint
	pGetCursor.Call(uintptr(unsafe.Pointer(&cursor)))
	work := monitorWork(cursor)
	w.x = min(max(int(cursor.X)-w.width, w.s(12)+int(work.Left)), int(work.Right)-w.width-w.s(12))
	w.y = min(max(int(cursor.Y)-w.height-w.s(12), int(work.Top)+w.s(12)), int(work.Bottom)-w.height-w.s(12))
	w.showAt(w.x, w.y)
}

func monitorWork(point panelPoint) panelRect {
	r := panelRect{point.X, point.Y, point.X + 1, point.Y + 1}
	monitor, _, _ := winUser.NewProc("MonitorFromRect").Call(uintptr(unsafe.Pointer(&r)), 2)
	info := panelMonitor{}
	info.Size = uint32(unsafe.Sizeof(info))
	winUser.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info)))
	if info.Work.Right == 0 {
		info.Work = panelRect{0, 0, 1920, 1080}
	}
	return info.Work
}

func (w *flyoutWindow) showAt(x, y int) {
	w.visible = true
	w.x = x
	w.y = y
	pPosition.Call(w.hwnd, 0, uintptr(x), uintptr(y), uintptr(w.width), uintptr(w.height), 0x0040)
	pShow.Call(w.hwnd, 5)
	pForeground.Call(w.hwnd)
	pInvalidate.Call(w.hwnd, 0, 0)
}
func (w *flyoutWindow) hide() {
	w.cancelEdit()
	w.visible = false
	pShow.Call(w.hwnd, 0)
}
func (p *windowsPanel) hide() { p.sub.hide(); p.main.hide() }

func (w *flyoutWindow) rebuild() {
	model := w.panel.snapshot()
	if w.sub {
		w.rows = w.subRows(model)
	} else {
		w.rows = w.mainRows(model)
	}
	w.width = w.s(570)
	if w.sub {
		w.width = w.s(290)
	}
	total := w.s(12)
	for _, row := range w.rows {
		total += w.s(row.height)
	}
	var cursor panelPoint
	pGetCursor.Call(uintptr(unsafe.Pointer(&cursor)))
	work := monitorWork(cursor)
	w.height = min(total, int(work.Bottom-work.Top)-w.s(28))
	w.scroll = min(w.scroll, max(0, total-w.height))
	y := w.s(6) - w.scroll
	for i := range w.rows {
		w.rows[i].rect = panelRect{int32(w.s(6)), int32(y), int32(w.width - w.s(6)), int32(y + w.s(w.rows[i].height))}
		y += w.s(w.rows[i].height)
	}
	if w.hover >= len(w.rows) {
		w.hover = -1
	}
	if w.visible {
		w.y = min(w.y, int(work.Bottom)-w.height-w.s(8))
		pPosition.Call(w.hwnd, 0, uintptr(w.x), uintptr(w.y), uintptr(w.width), uintptr(w.height), 0x0014)
	}
}

func (w *flyoutWindow) mainRows(m panelSnapshot) []panelRow {
	count := len(m.NICs)
	label := m.State
	if m.On {
		working := 0
		for _, n := range m.NICs {
			if n.State == "up" || n.State6 == "up" {
				working++
			}
		}
		switch working {
		case 0:
			label = "Checking internet connections"
		case 1:
			label = "1 connection active"
		default:
			label = fmt.Sprintf("%d connections merged", working)
		}
	}
	icon := "\ue73e"
	if !m.On {
		icon = "\ue769"
	}
	detail := fmt.Sprintf("Total ↓%s  ↑%s Mbps", mbps(m.Down), mbps(m.Up))
	if m.Error != "" {
		label = "Action needs attention"
		detail = m.Error
		icon = "\ue7ba"
	}
	rows := []panelRow{{label: label, detail: detail, icon: icon, kind: "header", height: 68}}
	rows = append(rows, panelRow{separator: true, height: 8})
	seen := map[string]bool{}
	for _, s := range m.NICs {
		seen[s.Name] = true
		info := m.providers[s.Name]
		name := provider.DisplayName(info.Name)
		if name == "" {
			name = s.Name
		}
		if alias := w.panel.prefs.Aliases[s.Name]; alias != "" {
			name = alias
		}
		glyph := "\ue839"
		for _, h := range m.hardware {
			if h.Name == s.Name && h.Wireless {
				glyph = "\ue701"
			}
		}
		detail := fmt.Sprintf("%s  ·  %d flows", s.Name, s.Active)
		if s.State != "up" && s.State6 != "up" {
			detail += "  ·  " + s.State
		}
		if s.LimitMbps > 0 {
			detail += "  ·  limit " + limitText(s.LimitMbps)
		}
		if s.State6 == "up" {
			detail += "  ·  IPv6"
		}
		height := 64
		if w.panel.prefs.ShowPublicIPs {
			ips := []string{}
			if info.IPv4 != "" {
				ips = append(ips, info.IPv4)
			}
			if info.IPv6 != "" {
				ips = append(ips, info.IPv6)
			}
			if len(ips) > 0 {
				detail += "\n" + strings.Join(ips, "   ")
				height = 82
			}
		}
		rows = append(rows, panelRow{label: name, detail: detail, icon: glyph, logo: info.Logo, right: fmt.Sprintf("↓%s  ↑%s Mbps", mbps(s.LinkIn), mbps(s.LinkOut)), kind: "adaptermenu", name: s.Name, height: height})
	}
	for _, h := range m.hardware {
		if seen[h.Name] || h.Enabled {
			continue
		}
		glyph := "\ue839"
		if h.Wireless {
			glyph = "\ue701"
		}
		rows = append(rows, panelRow{label: h.Name, detail: "Adapter disabled · Click to enable", icon: glyph, kind: "adaptermenu", name: h.Name, height: 64})
	}
	if count == 0 {
		rows = append(rows, panelRow{label: "No internet connections detected", detail: "Connect Wi-Fi, Ethernet or a phone hotspot", icon: "\ue774", disabled: true, height: 64})
	}
	rows = append(rows,
		panelRow{separator: true, height: 12},
		panelRow{label: "Use all adapters", detail: "IPv6 only when every connection supports it", icon: "\ue774", checked: m.V6Auto, kind: "ipv6", height: 52},
		panelRow{label: "Pause when only one connection works", icon: "\ue71b", checked: m.AutoPause, kind: "auto", height: 42},
		panelRow{label: "Start at sign-in", icon: "\ue77b", checked: m.SignIn, kind: "signin", height: 42},
		panelRow{label: "Show public IP addresses", icon: "\ue890", checked: w.panel.prefs.ShowPublicIPs, kind: "ips", height: 42},
		panelRow{separator: true, height: 12})
	mergeLabel, mergeIcon := "Start merging", "\ue768"
	if m.On {
		mergeLabel, mergeIcon = "Pause merging", "\ue769"
	}
	rows = append(rows, panelRow{label: mergeLabel, icon: mergeIcon, kind: "merge", height: 44})
	version := versionLabel(m.Version)
	if m.latest != "" {
		version = updateLabel(m.latest)
	}
	rows = append(rows, panelRow{label: version, icon: "\ue946", kind: "version", height: 40}, panelRow{label: "Quit", icon: "\ue7e8", kind: "quit", height: 44})
	return rows
}

func (w *flyoutWindow) subRows(m panelSnapshot) []panelRow {
	label := w.adapter
	if alias := w.panel.prefs.Aliases[w.adapter]; alias != "" {
		label = alias
	}
	rows := []panelRow{{label: label, detail: "Connection speed limit", icon: "\ue72b", kind: "back", height: 60}}
	var cap float64
	for _, s := range m.NICs {
		if s.Name == w.adapter {
			cap = s.LimitMbps
		}
	}
	// Preserve small caps and add the higher steps shown in the reference.
	steps := []float64{5, 10, 20, 50, 100, 200, 300, 500, 1000}
	if cap > 0 {
		found := false
		for _, v := range steps {
			if v == cap {
				found = true
			}
		}
		if !found {
			steps = append(steps, cap)
			sort.Float64s(steps)
		}
	}
	steps = append(steps, 0)
	for _, speed := range steps {
		text := limitText(speed)
		if speed == 0 {
			text = "Unlimited"
		}
		rows = append(rows, panelRow{label: text, checked: math.Abs(cap-speed) < 0.00001, kind: "limit", name: w.adapter, value: speed, height: 32})
	}
	enabled := true
	for _, h := range m.hardware {
		if h.Name == w.adapter {
			enabled = h.Enabled
		}
	}
	label = "Disable adapter"
	value := float64(0)
	if !enabled {
		label = "Enable adapter"
		value = 1
	}
	rows = append(rows, panelRow{separator: true, height: 12}, panelRow{label: label, icon: "\ue711", kind: "adapter", name: w.adapter, value: value, height: 42}, panelRow{label: "Rename in net-merge", icon: "\ue8ac", kind: "rename", name: w.adapter, height: 42})
	return rows
}

func (w *flyoutWindow) hit(x, y int) int {
	if x < 0 || x >= w.width || y < 0 || y >= w.height {
		return -1
	}
	for i, row := range w.rows {
		if row.separator || row.disabled || row.kind == "header" {
			continue
		}
		if y >= int(row.rect.Top) && y < int(row.rect.Bottom) {
			return i
		}
	}
	return -1
}

func (w *flyoutWindow) move(direction int) {
	if len(w.rows) == 0 {
		return
	}
	index := w.hover
	for range len(w.rows) {
		index = (index + direction + len(w.rows)) % len(w.rows)
		row := w.rows[index]
		if !row.separator && !row.disabled && row.kind != "header" {
			w.hover = index
			if row.rect.Top < 0 {
				w.scroll = max(0, w.scroll+int(row.rect.Top)-w.s(6))
				w.rebuild()
			}
			if row.rect.Bottom > int32(w.height) {
				w.scroll += int(row.rect.Bottom) - w.height + w.s(6)
				w.rebuild()
			}
			pInvalidate.Call(w.hwnd, 0, 0)
			return
		}
	}
}

func (w *flyoutWindow) activate(index int) {
	if index < 0 || index >= len(w.rows) {
		return
	}
	row := w.rows[index]
	switch row.kind {
	case "adaptermenu":
		sub := w.panel.sub
		sub.adapter = row.name
		sub.hover = -1
		sub.scroll = 0
		sub.theme()
		sub.rebuild()
		work := monitorWork(panelPoint{int32(w.x), int32(w.y)})
		x := w.x + w.width + w.s(6)
		if x+sub.width > int(work.Right) {
			x = w.x - sub.width - w.s(6)
		}
		x = max(x, int(work.Left)+w.s(6))
		y := min(w.y+int(row.rect.Top), int(work.Bottom)-sub.height-w.s(8))
		sub.showAt(x, max(y, int(work.Top)+w.s(8)))
	case "ips":
		w.panel.prefs.ShowPublicIPs = !w.panel.prefs.ShowPublicIPs
		w.panel.savePrefs()
		w.rebuild()
		pInvalidate.Call(w.hwnd, 0, 0)
	case "back":
		w.hide()
		pForeground.Call(w.panel.main.hwnd)
	case "rename":
		w.beginEdit()
	case "save":
		w.saveEdit()
	case "cancel":
		w.cancelEdit()
		w.rebuild()
		pInvalidate.Call(w.hwnd, 0, 0)
	case "limit":
		w.panel.emit(nativeAction{kind: row.kind, name: row.name, value: row.value})
		w.hide()
		pForeground.Call(w.panel.main.hwnd)
	default:
		w.panel.emit(nativeAction{kind: row.kind, name: row.name, value: row.value})
		w.panel.hide()
	}
}

func (p *windowsPanel) savePrefs() {
	if err := state.SaveTrayPreferences(p.dir, p.prefs); err != nil {
		logx.Printf("tray preferences: %v\n", err)
	}
}

func (w *flyoutWindow) paint() {
	var ps panelPaint
	dc, _, _ := pBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	var client panelRect
	pGetClient.Call(w.hwnd, uintptr(unsafe.Pointer(&client)))
	width, height := int(client.Right), int(client.Bottom)
	if width < 1 || height < 1 {
		return
	}
	buffer, _, _ := winGDI.NewProc("CreateCompatibleDC").Call(dc)
	bitmap, _, _ := winGDI.NewProc("CreateCompatibleBitmap").Call(dc, uintptr(width), uintptr(height))
	old, _, _ := pSelect.Call(buffer, bitmap)
	defer func() { pSelect.Call(buffer, old); pDeleteObject.Call(bitmap); winGDI.NewProc("DeleteDC").Call(buffer) }()
	w.fill(buffer, client, w.palette.background, 0)
	pBackgroundMode.Call(buffer, 1)
	for i, row := range w.rows {
		r := row.rect
		if r.Bottom < 0 || r.Top >= int32(height) {
			continue
		}
		if row.separator {
			line := panelRect{int32(w.s(16)), r.Top + int32(w.s(row.height/2)), int32(width - w.s(16)), r.Top + int32(w.s(row.height/2)) + 1}
			w.fill(buffer, line, w.palette.line, 0)
			continue
		}
		if row.kind == "header" {
			w.fill(buffer, r, w.palette.header, w.s(7))
		} else if i == w.hover {
			w.fill(buffer, r, w.palette.hover, w.s(6))
		}
		center := (int(r.Top) + int(r.Bottom)) / 2
		iconRect := panelRect{int32(w.s(20)), int32(center - w.s(16)), int32(w.s(48)), int32(center + w.s(16))}
		if row.logo != nil {
			w.logo(buffer, row.logo, iconRect)
		} else if row.icon != "" {
			color := w.palette.text
			if row.kind == "header" {
				color = w.palette.green
			}
			w.text(buffer, row.icon, iconRect, 3, color, 0x0001|0x0004|0x0020)
		}
		left := w.s(60)
		right := width - w.s(44)
		if row.kind == "limit" {
			left = w.s(48)
		}
		if row.right != "" {
			right = width - w.s(190)
			speed := panelRect{int32(right), r.Top + int32(w.s(8)), int32(width - w.s(36)), r.Top + int32(w.s(32))}
			w.text(buffer, row.right, speed, 0, w.palette.text, 0x0002|0x0004|0x0020)
		}
		font, color := 0, w.palette.text
		if row.kind == "header" {
			font = 2
			color = w.palette.accent
		}
		if row.kind == "version" {
			font = 1
			color = w.palette.muted
		}
		if row.detail != "" {
			labelRect := panelRect{int32(left), r.Top + int32(w.s(8)), int32(right), r.Top + int32(w.s(33))}
			w.text(buffer, row.label, labelRect, font, color, 0x0004|0x0020|0x8000)
			detailRect := panelRect{int32(left), r.Top + int32(w.s(33)), int32(width - w.s(38)), r.Bottom - int32(w.s(4))}
			w.text(buffer, row.detail, detailRect, 1, w.palette.muted, 0x0010|0x8000)
		} else {
			labelRect := panelRect{int32(left), r.Top, int32(right), r.Bottom}
			w.text(buffer, row.label, labelRect, font, color, 0x0004|0x0020|0x8000)
		}
		if row.checked {
			check := panelRect{int32(width - w.s(34)), r.Top, int32(width - w.s(14)), r.Bottom}
			if row.kind == "limit" {
				check = panelRect{int32(w.s(16)), r.Top, int32(w.s(36)), r.Bottom}
			}
			w.text(buffer, "\ue73e", check, 3, w.palette.accent, 0x0001|0x0004|0x0020)
		}
		if row.kind == "adaptermenu" {
			arrow := panelRect{int32(width - w.s(30)), r.Top, int32(width - w.s(12)), r.Bottom}
			w.text(buffer, "\ue76c", arrow, 3, w.palette.muted, 0x0001|0x0004|0x0020)
		}
	}
	winGDI.NewProc("BitBlt").Call(dc, 0, 0, uintptr(width), uintptr(height), buffer, 0, 0, 0x00cc0020)
}

func (w *flyoutWindow) fill(dc uintptr, r panelRect, color uint32, radius int) {
	brush, _, _ := pBrush.Call(uintptr(color))
	defer pDeleteObject.Call(brush)
	if radius == 0 {
		pFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), brush)
		return
	}
	old, _, _ := pSelect.Call(dc, brush)
	pen, _, _ := pStockObject.Call(8) // NULL_PEN
	oldPen, _, _ := pSelect.Call(dc, pen)
	pRoundRect.Call(dc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), uintptr(radius*2), uintptr(radius*2))
	pSelect.Call(dc, oldPen)
	pSelect.Call(dc, old)
}
func (w *flyoutWindow) text(dc uintptr, value string, r panelRect, font int, color uint32, flags uintptr) {
	old, _, _ := pSelect.Call(dc, w.fonts[font])
	defer pSelect.Call(dc, old)
	pTextColor.Call(dc, uintptr(color))
	text := windows.StringToUTF16(value)
	pDrawText.Call(dc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)-1), uintptr(unsafe.Pointer(&r)), flags|0x0800) // DT_NOPREFIX
}
func (w *flyoutWindow) logo(dc uintptr, img image.Image, r panelRect) {
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	if iw < 1 || ih < 1 || iw > 256 || ih > 256 {
		return
	}
	bg := w.palette.background
	pixels := make([]byte, iw*ih*4)
	for y := 0; y < ih; y++ {
		for x := 0; x < iw; x++ {
			red, green, blue, alpha := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			blend := func(v, b uint32) byte { return byte((v + (b*257)*(65535-alpha)/65535) / 257) }
			i := (y*iw + x) * 4
			pixels[i] = blend(blue, (bg>>16)&255)
			pixels[i+1] = blend(green, (bg>>8)&255)
			pixels[i+2] = blend(red, bg&255)
		}
	}
	size := w.s(26)
	x, y := int(r.Left)+(int(r.Right-r.Left)-size)/2, int(r.Top)+(int(r.Bottom-r.Top)-size)/2
	info := panelBitmap{Width: int32(iw), Height: -int32(ih), Planes: 1, BitCount: 32}
	info.Size = uint32(unsafe.Sizeof(info))
	pStretchDIB.Call(dc, uintptr(x), uintptr(y), uintptr(size), uintptr(size), 0, 0, uintptr(iw), uintptr(ih), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0, 0x00cc0020)
}

// Rename changes only the label shown by net-merge; system interface names and
// saved speed limits remain stable. The edit field is a real Windows EDIT control.
var panelEditProc = syscall.NewCallback(func(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	parent, _, _ := winUser.NewProc("GetParent").Call(hwnd)
	v, ok := panelWindows.Load(parent)
	if ok {
		w := v.(*flyoutWindow)
		if message == 0x0100 && (wp == 0x0d || wp == 0x1b) {
			if wp == 0x0d {
				w.saveEdit()
			} else {
				w.cancelEdit()
				w.rebuild()
				pInvalidate.Call(w.hwnd, 0, 0)
			}
			return 0
		}
		result, _, _ := winUser.NewProc("CallWindowProcW").Call(w.editOldProc, hwnd, uintptr(message), wp, lp)
		return result
	}
	result, _, _ := pDefaultWindow.Call(hwnd, uintptr(message), wp, lp)
	return result
})

func (w *flyoutWindow) beginEdit() {
	w.editing = true
	w.rows = []panelRow{{label: "Rename connection", detail: "Display name in net-merge only", icon: "\ue8ac", kind: "header", height: 64}, {height: 56, disabled: true}, {label: "Save name", icon: "\ue74e", kind: "save", height: 44}, {label: "Cancel", icon: "\ue711", kind: "cancel", height: 44}}
	w.height = w.s(220)
	w.scroll = 0
	y := w.s(6)
	for i := range w.rows {
		w.rows[i].rect = panelRect{int32(w.s(6)), int32(y), int32(w.width - w.s(6)), int32(y + w.s(w.rows[i].height))}
		y += w.s(w.rows[i].height)
	}
	pPosition.Call(w.hwnd, 0, uintptr(w.x), uintptr(w.y), uintptr(w.width), uintptr(w.height), 0x0014)
	name := w.panel.prefs.Aliases[w.adapter]
	if name == "" {
		name = w.adapter
	}
	input, _, _ := pCreateWindow.Call(0x0200, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("EDIT"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(name))), 0x50010080,
		uintptr(w.s(18)), uintptr(w.s(76)), uintptr(w.width-w.s(36)), uintptr(w.s(30)), w.hwnd, 0, 0, 0)
	w.input = input
	if input != 0 {
		winUser.NewProc("SendMessageW").Call(input, 0x0030, w.fonts[0], 1)
		winUser.NewProc("SendMessageW").Call(input, 0x00c5, 100, 0) // EM_SETLIMITTEXT
		proc := winUser.NewProc("SetWindowLongPtrW")
		if unsafe.Sizeof(uintptr(0)) == 4 {
			proc = winUser.NewProc("SetWindowLongW")
		}
		w.editOldProc, _, _ = proc.Call(input, ^uintptr(3), panelEditProc) // GWLP_WNDPROC = -4
		winUser.NewProc("SetFocus").Call(input)
		winUser.NewProc("SendMessageW").Call(input, 0x00b1, 0, ^uintptr(0))
	}
	pInvalidate.Call(w.hwnd, 0, 0)
}
func (w *flyoutWindow) cancelEdit() {
	if w.input != 0 {
		winUser.NewProc("DestroyWindow").Call(w.input)
		w.input = 0
	}
	w.editing = false
}
func (w *flyoutWindow) saveEdit() {
	if w.input == 0 {
		return
	}
	var buf [256]uint16
	winUser.NewProc("GetWindowTextW").Call(w.input, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	name := strings.TrimSpace(string(utf16.Decode(buf[:nullIndex(buf[:])])))
	if name == "" || name == w.adapter {
		delete(w.panel.prefs.Aliases, w.adapter)
	} else {
		w.panel.prefs.Aliases[w.adapter] = name
	}
	w.panel.savePrefs()
	w.cancelEdit()
	w.rebuild()
	pInvalidate.Call(w.hwnd, 0, 0)
	w.panel.main.rebuild()
	pInvalidate.Call(w.panel.main.hwnd, 0, 0)
}
func nullIndex(buf []uint16) int {
	for i, v := range buf {
		if v == 0 {
			return i
		}
	}
	return len(buf)
}
