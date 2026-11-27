package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The speed icon is a second notification-area icon, beside the app's. It is not systray's: systray.SetIcon
// writes every new image to a temporary file and keeps a handle for each one, and this icon changes every
// few seconds. This icon is ours: one hidden window on its own thread owns it, and each picture is an HICON
// that is destroyed as soon as the next one has replaced it, so a day of updates costs no handles.
//
// The window is a hidden top-level one, not a message-only window, because only top-level windows are told
// "TaskbarCreated" when Explorer restarts, and then the icon has to be added again.

// speedIconID names the icon among this window's icons.
const speedIconID = 0x4E4D

var (
	pShellNotifyIcon          = shell32.NewProc("Shell_NotifyIconW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pDestroyIcon              = user32.NewProc("DestroyIcon")
	pRegisterClassEx          = user32.NewProc("RegisterClassExW")
	pCreateWindowEx           = user32.NewProc("CreateWindowExW")
	pDefWindowProc            = user32.NewProc("DefWindowProcW")
	pGetMessage               = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessage          = user32.NewProc("DispatchMessageW")
	pPostMessage              = user32.NewProc("PostMessageW")
	pSendMessageTimeout       = user32.NewProc("SendMessageTimeoutW")
	pRegisterWindowMessage    = user32.NewProc("RegisterWindowMessageW")
	pFindWindow               = user32.NewProc("FindWindowW")
	pGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pGetSystemMetricsForDpi   = user32.NewProc("GetSystemMetricsForDpi")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pGetModuleHandle          = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifIcon   = 0x2
	nifTip    = 0x4

	wmDestroy       = 0x0002
	wmEndSession    = 0x0016
	wmSettingChange = 0x001A
	wmDisplayChange = 0x007E
	wmThemeChanged  = 0x031A
	wmSpeedApply    = 0x8001 // make the icon what was last asked for

	wsPopup          = 0x80000000
	wsExToolWindow   = 0x00000080
	smtoBlock        = 0x1
	smtoAbortIfHung  = 0x2
	smCxSmIcon       = 49
	iconResourceVer3 = 0x00030000
)

// notifyIconData is NOTIFYICONDATAW (the Windows Vista and later layout, 976 bytes on 64-bit Windows).
type notifyIconData struct {
	size             uint32
	hwnd             uintptr
	id               uint32
	flags            uint32
	callbackMessage  uint32
	icon             uintptr
	tip              [128]uint16
	state, stateMask uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guid             windows.GUID
	balloonIcon      uintptr
}

type wndClassEx struct {
	size, style                 uint32
	proc                        uintptr
	clsExtra, wndExtra          int32
	inst, icon, cursor, bg      uintptr
	menuName, className, iconSm uintptr
}

type msg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	pt             struct{ x, y int32 }
	_              uint32
}

// speedState is what the tray last asked the icon to show.
type speedState struct {
	shown    bool
	down, up string
	merging  bool
}

type speedIcon struct {
	once    sync.Once
	hwnd    atomic.Uintptr
	pending atomic.Bool // an update is queued, so another need not be
	taskbar uint32      // the "TaskbarCreated" message

	mu   sync.Mutex
	want speedState

	// Only the icon's own thread touches these.
	hicon uintptr // the picture the notification area has now
	added bool    // whether the notification area has our icon
	shown string  // what that picture is of, so an unchanged icon is not drawn again
}

var speed = &speedIcon{}

// setTraySpeed shows the speed in a second tray icon beside the app's; an empty down removes it. merging
// picks the colours.
func setTraySpeed(down, up string, merging bool) {
	speed.set(speedState{shown: down != "", down: down, up: up, merging: merging})
}

// removeTraySpeed takes the icon away, and returns once it is gone: it is for when the tray quits.
func removeTraySpeed() { speed.set(speedState{}) }

func (s *speedIcon) set(w speedState) {
	s.mu.Lock()
	s.want = w
	s.mu.Unlock()
	if !w.shown {
		if s.hwnd.Load() != 0 { // nothing was ever shown if the window was never made
			s.send() // not posted: on quit the process may end before a posted message is handled
		}
		return
	}
	if !s.start() {
		return
	}
	if !s.pending.Swap(true) {
		pPostMessage.Call(s.hwnd.Load(), wmSpeedApply, 0, 0)
	}
}

// send has the icon's thread bring the icon up to date, and waits for it.
func (s *speedIcon) send() {
	var res uintptr
	pSendMessageTimeout.Call(s.hwnd.Load(), wmSpeedApply, 0, 0, smtoBlock|smtoAbortIfHung, 3000, uintptr(unsafe.Pointer(&res)))
}

func (s *speedIcon) start() bool {
	s.once.Do(func() {
		ready := make(chan struct{})
		go s.loop(ready)
		<-ready
	})
	return s.hwnd.Load() != 0
}

func (s *speedIcon) loop(ready chan struct{}) {
	runtime.LockOSThread() // a window belongs to the thread that made it
	signal := sync.OnceFunc(func() { close(ready) })
	defer signal() // start waits for this, whether or not the window could be made
	className, _ := windows.UTF16PtrFromString("NetMergeSpeedIcon")
	inst, _, _ := pGetModuleHandle.Call(0)
	wc := wndClassEx{proc: windows.NewCallback(speedWndProc), inst: inst, className: uintptr(unsafe.Pointer(className))}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return
	}
	name, _ := windows.UTF16PtrFromString("net-merge speed")
	h, _, _ := pCreateWindowEx.Call(wsExToolWindow, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(name)),
		wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if h == 0 {
		return
	}
	tb, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(tb)))
	s.taskbar = uint32(r)
	s.hwnd.Store(h)
	signal()
	var m msg
	for {
		if r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func speedWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	s := speed
	switch {
	case message == wmSpeedApply:
		s.pending.Store(false)
		s.apply()
		return 0
	case s.taskbar != 0 && uint32(message) == s.taskbar:
		// Explorer started again, and its notification area is empty: put the icon back.
		s.added = false
		s.apply()
		return 0
	case message == wmSettingChange, message == wmThemeChanged, message == wmDisplayChange:
		s.apply() // the taskbar's colours or the scale may have changed: draw it again if so
	case message == wmEndSession && wParam != 0, message == wmDestroy:
		s.remove()
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

// apply makes the notification area show what was last asked for.
func (s *speedIcon) apply() {
	s.mu.Lock()
	w := s.want
	s.mu.Unlock()
	if !w.shown {
		s.remove()
		return
	}
	dark, size := taskbarIsDark(), trayIconSize()
	key := fmt.Sprint(w, dark, size)
	if s.added && key == s.shown {
		return
	}
	h := hiconFromImage(speedIconImage(w.down, w.up, w.merging, dark, size))
	if h == 0 {
		return // keep the icon that is there
	}
	nid := s.notifyData(h, speedTooltip(w.down, w.up, w.merging))
	ok := false
	if s.added {
		ok = shellNotify(nimModify, &nid)
	}
	if !ok { // not there yet, or Explorer lost it: add it
		ok = shellNotify(nimAdd, &nid)
	}
	if !ok {
		pDestroyIcon.Call(h) // Explorer is not there; it will say so when it is (TaskbarCreated)
		return
	}
	old := s.hicon
	s.hicon, s.added, s.shown = h, true, key
	if old != 0 {
		pDestroyIcon.Call(old) // the notification area has the new picture now
	}
}

// remove takes the icon out of the notification area and frees its picture.
func (s *speedIcon) remove() {
	if s.added {
		nid := s.notifyData(0, "")
		shellNotify(nimDelete, &nid)
		s.added = false
	}
	if s.hicon != 0 {
		pDestroyIcon.Call(s.hicon)
		s.hicon = 0
	}
	s.shown = ""
}

func (s *speedIcon) notifyData(icon uintptr, tip string) notifyIconData {
	nid := notifyIconData{hwnd: s.hwnd.Load(), id: speedIconID, flags: nifIcon | nifTip, icon: icon}
	nid.size = uint32(unsafe.Sizeof(nid))
	u := utf16.Encode([]rune(tip))
	copy(nid.tip[:len(nid.tip)-1], u)
	return nid
}

func shellNotify(command uintptr, nid *notifyIconData) bool {
	r, _, _ := pShellNotifyIcon.Call(command, uintptr(unsafe.Pointer(nid)))
	return r != 0
}

// hiconFromImage makes an icon from a picture. Windows reads it as a PNG, which keeps the soft edges. The
// caller owns the icon and must DestroyIcon it.
func hiconFromImage(img *image.NRGBA) uintptr {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return 0
	}
	b := buf.Bytes()
	size := img.Bounds().Dx()
	h, _, _ := pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 1, iconResourceVer3, uintptr(size), uintptr(size), 0)
	return h
}

// taskbarIsDark reads the taskbar's theme (not the apps'): they are set apart in Windows 10 and 11. Without
// the setting, as on older Windows, the taskbar is dark.
func taskbarIsDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("SystemUsesLightTheme")
	return err != nil || v == 0
}

// trayIconSize is the size of a small icon on the taskbar's own display: 16 at 100%, 20 at 125%, 24 at 150%,
// 32 at 200%.
func trayIconSize() int {
	dpi := uintptr(96)
	cls, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	if tray, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0); tray != 0 && pGetDpiForWindow.Find() == nil {
		if d, _, _ := pGetDpiForWindow.Call(tray); d != 0 {
			dpi = d
		}
	} else if pGetDpiForSystem.Find() == nil {
		if d, _, _ := pGetDpiForSystem.Call(); d != 0 {
			dpi = d
		}
	}
	if pGetSystemMetricsForDpi.Find() == nil {
		if r, _, _ := pGetSystemMetricsForDpi.Call(smCxSmIcon, dpi); r != 0 {
			return int(r)
		}
	}
	if r, _, _ := pGetSystemMetrics.Call(smCxSmIcon); r != 0 {
		return int(r)
	}
	return int(16 * dpi / 96)
}
