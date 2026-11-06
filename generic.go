package main

// Support for any mouse, whatever the brand. Windows tells us about the back,
// forward and middle buttons (see hook_windows.go), and pointer speed, pointer
// precision and scroll lines are Windows settings that apply to every mouse.

import (
	"strings"
	"time"
)

// SysMouse reads and changes Windows' own mouse settings.
type SysMouse interface {
	Speed() int // 1..20
	SetSpeed(v int, persist bool) error
	Accel() bool
	SetAccel(on bool) error
	ScrollLines() int
	SetScrollLines(n int) error
	Animations() bool
	SetAnimations(on bool) error
}

type SysView struct {
	Speed       int  `json:"speed"`
	Accel       bool `json:"accel"`
	ScrollLines int  `json:"scrollLines"`
	Animations  bool `json:"animations"`
}

// EnableGeneric turns on support for any mouse.
func (e *Engine) EnableGeneric(sys SysMouse, name string) {
	e.sys = sys
	e.generic = &Device{key: "generic", Name: name, Conn: "system", Online: true, Ready: true, Battery: -1,
		feat: map[uint16]byte{}, cids: map[uint16]cidInfo{}, diverted: map[uint16]byte{},
		pressed: map[uint16]bool{}, held: map[uint16]func(){}}
	e.mu.Lock()
	cfg := e.cfg.Clone()
	e.mu.Unlock()
	e.applySystem(nil, cfg)
}

func (e *Engine) SetAnimations(on bool) error {
	if e.sys == nil {
		return nil
	}
	logf("windows animation effects -> %v", on)
	return e.sys.SetAnimations(on)
}

func (e *Engine) Sys() *SysView {
	if e.sys == nil {
		return nil
	}
	return &SysView{Speed: e.sys.Speed(), Accel: e.sys.Accel(), ScrollLines: e.sys.ScrollLines(), Animations: e.sys.Animations()}
}

// applySystem pushes the Windows mouse settings that changed.
func (e *Engine) applySystem(old, c *Config) {
	if e.sys == nil {
		return
	}
	if c.WinSpeed >= 1 && c.WinSpeed <= 20 && (old == nil || old.WinSpeed != c.WinSpeed) {
		if err := e.sys.SetSpeed(c.WinSpeed, true); err != nil {
			logf("windows pointer speed: %v", err)
		}
	}
	if c.WinAccel != nil && (old == nil || old.WinAccel == nil || *old.WinAccel != *c.WinAccel) {
		if err := e.sys.SetAccel(*c.WinAccel); err != nil {
			logf("windows pointer precision: %v", err)
		}
	}
	if c.ScrollLines >= 1 && (old == nil || old.ScrollLines != c.ScrollLines) {
		if err := e.sys.SetScrollLines(c.ScrollLines); err != nil {
			logf("windows scroll lines: %v", err)
		}
	}
}

var genericSaved int

// tempSysSpeed slows the pointer for precision mode on any mouse, and puts it back.
func (e *Engine) tempSysSpeed(on bool) {
	if e.sys == nil {
		return
	}
	if on {
		if genericSaved == 0 {
			genericSaved = e.sys.Speed()
		}
		e.mu.Lock()
		ratio := float64(e.cfg.PrecisionDPI) / 1600
		e.mu.Unlock()
		v := int(float64(genericSaved)*ratio + 0.5)
		if v < 1 {
			v = 1
		}
		_ = e.sys.SetSpeed(v, false)
	} else if genericSaved > 0 {
		_ = e.sys.SetSpeed(genericSaved, false)
		genericSaved = 0
	}
}

// GenericWants tells the Windows hook whether to take over a button press.
// It must be quick: it runs inside the hook.
func (e *Engine) GenericWants(key string) (take bool, gestures bool) {
	if e.generic == nil {
		return false, false
	}
	e.mu.Lock()
	paused := e.cfg.Paused
	e.mu.Unlock()
	if paused {
		return false, false
	}
	a := e.resolve(key)
	return a.Type != "default", a.Type == "gestures"
}

// GenericNeeded says whether any setting needs the Windows hook at all.
func (e *Engine) GenericNeeded() bool {
	if e.generic == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.Paused {
		return false
	}
	for _, k := range []string{"back", "forward", "middle"} {
		if e.cfg.Buttons[k].Type != "default" {
			return true
		}
		for _, p := range e.cfg.Profiles {
			if a, ok := p.Buttons[k]; ok && a.Type != "inherit" && a.Type != "default" && a.Type != "" {
				return true
			}
		}
	}
	return false
}

// GenericButton and GenericMove are posted by the hook.
func (e *Engine) GenericButton(key string, down bool) {
	cid := buttonCID[key]
	e.post(func() {
		d := e.generic
		if d == nil {
			return
		}
		if down {
			d.pressed[cid] = true
			e.buttonDown(d, cid)
		} else {
			delete(d.pressed, cid)
			e.buttonUp(d, cid)
		}
	})
}

func (e *Engine) GenericMove(dx, dy int) {
	e.post(func() {
		if d := e.generic; d != nil && d.gesture != nil {
			d.gesture.dx += dx
			d.gesture.dy += dy
		}
	})
}

func genericName(lang string) string {
	if strings.HasPrefix(lang, "ar") {
		return "أي ماوس"
	}
	return "Any mouse"
}

var _ = time.Second
