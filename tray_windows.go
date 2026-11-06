package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	menuOpen  = 1
	menuPause = 3
	menuExit  = 4
)

type Tray struct {
	hwnd           uintptr
	icon           uintptr
	taskbarCreated uint32
	app            *App
	wantUI         bool
}

var theTray *Tray

func copyUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func loadAppIcon(size int) uintptr {
	hinst := moduleHandle()
	h, _, _ := pLoadImageW.Call(uintptr(hinst), 1, 1 /*IMAGE_ICON*/, uintptr(size), uintptr(size), 0)
	return h
}

func NewTray(app *App) (*Tray, error) {
	t := &Tray{app: app}
	theTray = t
	hinst := moduleHandle()
	cls := u16("MishkTray")
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   syscall.NewCallback(trayWndProc),
		Instance:  hinst,
		ClassName: cls,
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, fmt.Errorf("RegisterClassEx: %v", err)
	}
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Mishk"))),
		0, 0, 0, 0, 0, 0, 0, uintptr(hinst), 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", err)
	}
	t.hwnd = hwnd
	sm, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	t.icon = loadAppIcon(int(sm))
	tc, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	t.taskbarCreated = uint32(tc)
	t.add()
	t.registerDeviceNotifications()
	return t, nil
}

func (t *Tray) nid() *notifyIconData {
	n := &notifyIconData{HWnd: t.hwnd, UID: 1}
	n.CbSize = uint32(unsafe.Sizeof(*n))
	return n
}

func (t *Tray) add() {
	n := t.nid()
	n.UFlags = nifMessage | nifIcon | nifTip
	n.UCallbackMessage = wmTray
	n.HIcon = t.icon
	copyUTF16(n.SzTip[:], t.tooltip())
	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(n)))
}

func (t *Tray) Remove() {
	n := t.nid()
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(n)))
}

func (t *Tray) tooltip() string {
	name, bat, online := t.app.engine.Summary()
	ar := t.app.engine.Config().Lang == "ar"
	if name == "" {
		if ar {
			return "Mishk - ما في ماوس متصل"
		}
		return "Mishk - no mouse connected"
	}
	if !online {
		if ar {
			return fmt.Sprintf("%s - نايم أو غير متصل", name)
		}
		return fmt.Sprintf("%s - asleep or disconnected", name)
	}
	if bat >= 0 {
		return fmt.Sprintf("%s - %d%%", name, bat)
	}
	return name
}

func (t *Tray) UpdateTooltip() {
	n := t.nid()
	n.UFlags = nifTip
	copyUTF16(n.SzTip[:], t.tooltip())
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(n)))
}

func (t *Tray) Notify(title, body string) {
	n := t.nid()
	n.UFlags = nifInfo
	copyUTF16(n.SzInfoTitle[:], title)
	copyUTF16(n.SzInfo[:], body)
	n.DwInfoFlags = 0x01 // NIIF_INFO
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(n)))
}

type devBroadcastDeviceInterface struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	ClassGUID  windows.GUID
	Name       [1]uint16
}

func (t *Tray) registerDeviceNotifications() {
	f := devBroadcastDeviceInterface{DeviceType: 5, ClassGUID: hidGUID()}
	f.Size = uint32(unsafe.Sizeof(f))
	pRegisterDeviceNotifyW.Call(t.hwnd, uintptr(unsafe.Pointer(&f)), 0)
}

func (t *Tray) showMenu() {
	ar := t.app.engine.Config().Lang == "ar"
	tr := func(a, e string) string {
		if ar {
			return a
		}
		return e
	}
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(flags uintptr, id uintptr, text string) {
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(u16(text))))
	}
	add(0, menuOpen, tr("فتح الإعدادات", "Open settings"))
	add(0x800, 0, "")
	add(1 /*MF_GRAYED*/, 0, t.tooltip())
	pauseFlags := uintptr(0)
	if t.app.engine.Config().Paused {
		pauseFlags = 8 // MF_CHECKED
	}
	add(pauseFlags, menuPause, tr("إيقاف مؤقت للأزرار", "Pause button remapping"))
	add(0x800, 0, "")
	add(0, menuExit, tr("خروج", "Exit"))
	pSetMenuDefaultItem.Call(m, menuOpen, 0)
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(t.hwnd)
	flags := uintptr(0x0100 /*TPM_RETURNCMD*/ | 0x0020 /*TPM_BOTTOMALIGN*/)
	if ar {
		flags |= 0x0008 // TPM_RIGHTALIGN
	}
	cmd, _, _ := pTrackPopupMenu.Call(m, flags, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	switch cmd {
	case menuOpen:
		t.app.requestUI()
	case menuPause:
		c := t.app.engine.Config()
		c.Paused = !c.Paused
		_ = t.app.engine.SetConfig(c)
		updateHook(t.app.engine, false)
		t.app.pushState()
	case menuExit:
		t.app.Quit()
	}
}

func trayWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	t := theTray
	if t == nil {
		r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	switch uint32(msg) {
	case wmTray:
		switch uint32(lp) {
		case wmLButtonUp, wmLButtonDblClk:
			t.app.requestUI()
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	case wmShowUI:
		t.app.requestUI()
		return 0
	case wmRefresh:
		t.UpdateTooltip()
		return 0
	case wmQuitApp:
		t.app.Quit()
		return 0
	case wmDeviceChange:
		if wp == 0x8000 || wp == 0x8004 || wp == 0x0007 {
			t.app.scanner.Trigger()
		}
		return 1
	case wmPowerBroadcast:
		if wp == 0x12 || wp == 0x07 { // resumed from sleep
			logf("system resumed from sleep")
			updateHook(t.app.engine, true)
			t.app.scanner.Trigger()
			t.app.engine.postAfter(3e9, func() { t.app.engine.Refresh() })
		}
		return 1
	}
	if t.taskbarCreated != 0 && uint32(msg) == t.taskbarCreated {
		t.add()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}
