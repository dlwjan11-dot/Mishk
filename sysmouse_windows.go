package main

import (
	"errors"
	"unsafe"
)

var pSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")

const (
	spiGetMouse            = 0x0003
	spiSetMouse            = 0x0004
	spiGetWheelScrollLines = 0x0068
	spiSetWheelScrollLines = 0x0069
	spiGetMouseSpeed       = 0x0070
	spiSetMouseSpeed       = 0x0071
	spifSave               = 0x01 | 0x02 // SPIF_UPDATEINIFILE | SPIF_SENDCHANGE
	spiGetClientAreaAnim   = 0x1042
	spiSetClientAreaAnim   = 0x1043
)

func spi(action, uiParam, pv, winIni uintptr) error {
	r, _, err := pSystemParametersInfoW.Call(action, uiParam, pv, winIni)
	if r == 0 {
		if err == nil {
			err = errors.New("SystemParametersInfo failed")
		}
		return err
	}
	return nil
}

// winSys is Windows' own mouse settings, shared by every mouse.
type winSys struct{}

func (winSys) Speed() int {
	var v int32
	if spi(spiGetMouseSpeed, 0, uintptr(unsafe.Pointer(&v)), 0) != nil {
		return 10
	}
	return int(v)
}

func (winSys) SetSpeed(v int, persist bool) error {
	if v < 1 {
		v = 1
	}
	if v > 20 {
		v = 20
	}
	flags := uintptr(0)
	if persist {
		flags = spifSave
	}
	return spi(spiSetMouseSpeed, 0, uintptr(v), flags)
}

func (winSys) Accel() bool {
	var p [3]int32
	if spi(spiGetMouse, 0, uintptr(unsafe.Pointer(&p[0])), 0) != nil {
		return false
	}
	return p[2] != 0
}

func (winSys) SetAccel(on bool) error {
	p := [3]int32{0, 0, 0}
	if on {
		p = [3]int32{6, 10, 1} // Windows defaults with "Enhance pointer precision" on
	}
	return spi(spiSetMouse, 0, uintptr(unsafe.Pointer(&p[0])), spifSave)
}

func (winSys) ScrollLines() int {
	var v uint32
	if spi(spiGetWheelScrollLines, 0, uintptr(unsafe.Pointer(&v)), 0) != nil {
		return 3
	}
	return int(v)
}

func (winSys) SetScrollLines(n int) error {
	if n < 1 {
		n = 1
	}
	return spi(spiSetWheelScrollLines, uintptr(n), 0, spifSave)
}

// Windows "Animation effects". Turning them off fixes pointer stutter for many people.
func (winSys) Animations() bool {
	var v int32
	if spi(spiGetClientAreaAnim, 0, uintptr(unsafe.Pointer(&v)), 0) != nil {
		return true
	}
	return v != 0
}

func (winSys) SetAnimations(on bool) error {
	v := uintptr(0)
	if on {
		v = 1
	}
	return spi(spiSetClientAreaAnim, 0, v, spifSave)
}
