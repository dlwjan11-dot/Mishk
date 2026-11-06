package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	hidDLL   = windows.NewLazySystemDLL("hid.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pFindWindowW              = user32.NewProc("FindWindowW")
	pLoadImageW               = user32.NewProc("LoadImageW")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetMenuDefaultItem       = user32.NewProc("SetMenuDefaultItem")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pIsIconic                 = user32.NewProc("IsIconic")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pRegisterDeviceNotifyW    = user32.NewProc("RegisterDeviceNotificationW")
	pSendInput                = user32.NewProc("SendInput")
	pMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
	pMessageBoxW              = user32.NewProc("MessageBoxW")
	pEnumWindows              = user32.NewProc("EnumWindows")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	pGetWindow                = user32.NewProc("GetWindow")
	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
	pHidDGetHidGuid           = hidDLL.NewProc("HidD_GetHidGuid")
	pHidDGetAttributes        = hidDLL.NewProc("HidD_GetAttributes")
	pHidDGetPreparsedData     = hidDLL.NewProc("HidD_GetPreparsedData")
	pHidDFreePreparsedData    = hidDLL.NewProc("HidD_FreePreparsedData")
	pHidPGetCaps              = hidDLL.NewProc("HidP_GetCaps")
	pHidDGetProductString     = hidDLL.NewProc("HidD_GetProductString")
)

const (
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmNull           = 0x0000
	wmCommand        = 0x0111
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	wmRButtonUp      = 0x0205
	wmContextMenu    = 0x007B
	wmDeviceChange   = 0x0219
	wmPowerBroadcast = 0x0218
	wmApp            = 0x8000

	wmTray     = wmApp + 1
	wmShowUI   = wmApp + 2
	wmRefresh  = wmApp + 3
	wmQuitApp  = wmApp + 4
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func messageBox(title, text string, flags uintptr) int {
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
	return int(r)
}

func shellOpen(target string, args string) {
	var a *uint16
	if args != "" {
		a = u16(args)
	}
	_ = windows.ShellExecute(0, u16("open"), u16(target), a, nil, windows.SW_SHOWNORMAL)
}

func setDarkTitleBar(hwnd uintptr, dark bool) {
	v := int32(0)
	if dark {
		v = 1
	}
	if pDwmSetWindowAttribute.Find() == nil {
		pDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&v)), 4)
	}
}

var _ = syscall.NewCallback

func moduleHandle() windows.Handle {
	var h windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &h)
	return h
}
