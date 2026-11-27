//go:build speedlive

// The live test puts the speed icon in the real notification area for a few seconds, so it needs a desktop
// session with Explorer, and only runs when asked: go test -tags speedlive -run TestSpeedIconLive ./internal/tray
package tray

import (
	"fmt"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// iconInTray asks Explorer whether it has the icon: a Shell_NotifyIcon "modify" that changes nothing is
// answered with FALSE for an icon that does not exist. (Shell_NotifyIconGetRect is not used: on Windows 11 it
// does not find an icon that sits in the overflow flyout, which is where a new one goes.)
func iconInTray() bool {
	nid := speed.notifyData(0, "")
	nid.flags = 0
	return shellNotify(nimModify, &nid)
}

// inTray waits briefly for Explorer to catch up, as it registers an icon a moment after Shell_NotifyIcon
// has returned, and says whether the icon is in the notification area.
func inTray(want bool) bool {
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if iconInTray() == want {
			return want
		}
	}
	return iconInTray()
}

func TestSpeedIconLive(t *testing.T) {
	cls, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	if tray, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0); tray == 0 {
		t.Skip("no taskbar: Explorer is not running")
	}
	flush := func() { speed.send() }

	setTraySpeed("126", "10", true)
	flush()
	if !inTray(true) {
		t.Fatal("the icon is not in the notification area after setTraySpeed")
	}
	t.Logf("icon is in the notification area; taskbar dark: %v, icon size: %d", taskbarIsDark(), trayIconSize())

	// Thousands of updates, each a new picture, the old one destroyed: the handle counts stay flat.
	update := func(i int) {
		setTraySpeed(Mbps(float64(i%1500)*125000), Mbps(float64(i%77)*125000), i%4 != 0)
		flush() // one at a time, so every picture is really made
	}
	for i := 0; i < 100; i++ {
		update(i)
	}
	gdi0, user0 := guiHandles()
	start := time.Now()
	const batches, perBatch = 6, 1000
	var gdiLast, userLast int
	for b := 1; b <= batches; b++ {
		for i := 0; i < perBatch; i++ {
			update(100 + (b-1)*perBatch + i)
		}
		gdiLast, userLast = guiHandles()
		t.Logf("after %4d updates: GDI objects %d, USER objects %d", b*perBatch, gdiLast, userLast)
	}
	t.Logf("%d updates in %v: GDI objects %d -> %d, USER objects %d -> %d", batches*perBatch, time.Since(start).Round(time.Millisecond), gdi0, gdiLast, user0, userLast)
	// A leak would add a handle or more per update, thousands in all. What is allowed is a few kept by
	// Windows for the thread, the window and the notification area on first use.
	if gdiLast-gdi0 > 4 || userLast-user0 > 8 {
		t.Errorf("handles grew over %d updates: GDI %d -> %d, USER %d -> %d", batches*perBatch, gdi0, gdiLast, user0, userLast)
	}
	if !inTray(true) {
		t.Fatal("the icon vanished during the updates")
	}

	// Explorer loses the icon without telling us (it restarted, or crashed): the next update puts it back...
	nid := speed.notifyData(0, "")
	shellNotify(nimDelete, &nid)
	if inTray(false) {
		t.Fatal("the test could not take the icon away")
	}
	setTraySpeed("127", "11", true)
	flush()
	if !inTray(true) {
		t.Error("an update after Explorer lost the icon did not bring it back")
	}

	// ...and so does the TaskbarCreated message that Explorer broadcasts when it starts again.
	shellNotify(nimDelete, &nid)
	if inTray(false) {
		t.Fatal("the test could not take the icon away")
	}
	var res uintptr
	pSendMessageTimeout.Call(speed.hwnd.Load(), uintptr(speed.taskbar), 0, 0, smtoBlock|smtoAbortIfHung, 3000, uintptr(unsafe.Pointer(&res)))
	if !inTray(true) {
		t.Error("TaskbarCreated did not bring the icon back")
	}

	// An empty download removes it, and the picture is freed.
	setTraySpeed("", "", false)
	if inTray(false) {
		t.Error("an empty download did not remove the icon")
	}
	setTraySpeed("5.0", "1.0", false) // paused colours; and it can come back after being removed
	flush()
	if !inTray(true) {
		t.Error("the icon did not come back after being removed")
	}

	// Quitting removes it, at once: the call returns after the icon is gone.
	removeTraySpeed()
	if inTray(false) {
		t.Error("removeTraySpeed left the icon in the notification area")
	}
	gdi2, user2 := guiHandles()
	t.Logf("after removing it: GDI %d, USER %d", gdi2, user2)
	if gdi2-gdi0 > 4 || user2-user0 > 8 {
		t.Errorf("handles after removing the icon: GDI %d -> %d, USER %d -> %d", gdi0, gdi2, user0, user2)
	}
	fmt.Println("speed icon live test finished")
}
