package main

// A low-level mouse hook so the back, forward and middle buttons of ANY mouse can be
// remapped. Logitech HID++ mice don't need it (their buttons come straight from the
// mouse), so it is only installed when a setting needs it, and the hook itself only
// decides "take it or leave it" and hands the work to the engine, so it stays fast
// and Windows never drops it for being slow.

import (
	"syscall"
	"unsafe"
)

var (
	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
)

type msllHook struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	Extra     uintptr
}

const (
	wmMouseMove   = 0x0200
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmXButtonDown = 0x020B
	wmXButtonUp   = 0x020C
)

var mouseHook struct {
	h       uintptr
	cb      uintptr
	e       *Engine
	taken   map[string]bool
	gesture string
	anchor  point
}

func hookProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && mouseHook.e != nil {
		info := *(**msllHook)(unsafe.Pointer(&lParam))
		if info.Flags&0x01 == 0 { // not injected (ours or anyone else's)
			if r, handled := hookHandle(uint32(wParam), info); handled {
				return r
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

func hookHandle(msg uint32, info *msllHook) (uintptr, bool) {
	e := mouseHook.e
	key, down := "", false
	switch msg {
	case wmMouseMove:
		if mouseHook.gesture != "" {
			dx := int(info.Pt.X - mouseHook.anchor.X)
			dy := int(info.Pt.Y - mouseHook.anchor.Y)
			if dx != 0 || dy != 0 {
				e.GenericMove(dx, dy)
			}
			return 1, true // hold the pointer still while swiping
		}
		return 0, false
	case wmMButtonDown, wmMButtonUp:
		key, down = "middle", msg == wmMButtonDown
	case wmXButtonDown, wmXButtonUp:
		switch info.MouseData >> 16 {
		case 1:
			key = "back"
		case 2:
			key = "forward"
		default:
			return 0, false
		}
		down = msg == wmXButtonDown
	default:
		return 0, false
	}
	if down {
		take, gestures := e.GenericWants(key)
		if !take {
			return 0, false
		}
		mouseHook.taken[key] = true
		if gestures {
			mouseHook.gesture = key
			mouseHook.anchor = info.Pt
		}
		e.GenericButton(key, true)
		return 1, true
	}
	if mouseHook.taken[key] {
		delete(mouseHook.taken, key)
		if mouseHook.gesture == key {
			mouseHook.gesture = ""
		}
		e.GenericButton(key, false)
		return 1, true
	}
	return 0, false
}

// updateHook installs or removes the hook to match the settings. Main thread only.
func updateHook(e *Engine, force bool) {
	need := e.GenericNeeded()
	if mouseHook.h != 0 && (!need || force) {
		pUnhookWindowsHookEx.Call(mouseHook.h)
		mouseHook.h = 0
		logf("mouse hook removed")
	}
	if need && mouseHook.h == 0 {
		if mouseHook.cb == 0 {
			mouseHook.cb = syscall.NewCallback(hookProc)
		}
		mouseHook.e = e
		mouseHook.taken = map[string]bool{}
		mouseHook.gesture = ""
		h, _, err := pSetWindowsHookExW.Call(14 /* WH_MOUSE_LL */, mouseHook.cb, uintptr(moduleHandle()), 0)
		if h == 0 {
			logf("mouse hook: %v", err)
			return
		}
		mouseHook.h = h
		logf("mouse hook installed (for non-Logitech mice)")
	}
}
