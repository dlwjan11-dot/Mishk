package main

import (
	"fmt"
	"strings"
)

// Actuator sends synthetic input to the OS (SendInput on Windows, a recorder in tests).
type Actuator interface {
	Key(vk uint16, down bool)
	MouseButton(btn string, down bool) // left right middle back forward
	Wheel(delta int, horizontal bool)
	Run(target string)
}

type KeyCombo []uint16 // modifiers first, then the main key

var vkNames = map[string]uint16{
	"ctrl": 0x11, "control": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5B, "meta": 0x5B,
	"tab": 0x09, "enter": 0x0D, "return": 0x0D, "esc": 0x1B, "escape": 0x1B, "space": 0x20,
	"backspace": 0x08, "delete": 0x2E, "del": 0x2E, "insert": 0x2D, "ins": 0x2D,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pgup": 0x21, "pagedown": 0x22, "pgdn": 0x22,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"printscreen": 0x2C, "prtsc": 0x2C, "capslock": 0x14, "pause": 0x13, "menu": 0x5D, "apps": 0x5D,
	";": 0xBA, "=": 0xBB, ",": 0xBC, "-": 0xBD, ".": 0xBE, "/": 0xBF, "`": 0xC0,
	"[": 0xDB, "\\": 0xDC, "]": 0xDD, "'": 0xDE,
	"plus": 0xBB, "minus": 0xBD, "comma": 0xBC, "period": 0xBE,
	"volumeup": 0xAF, "volumedown": 0xAE, "volumemute": 0xAD, "mute": 0xAD,
	"medianext": 0xB0, "mediaprev": 0xB1, "mediastop": 0xB2, "mediaplaypause": 0xB3, "playpause": 0xB3,
	"browserback": 0xA6, "browserforward": 0xA7, "browserrefresh": 0xA8,
	"num0": 0x60, "num1": 0x61, "num2": 0x62, "num3": 0x63, "num4": 0x64, "num5": 0x65,
	"num6": 0x66, "num7": 0x67, "num8": 0x68, "num9": 0x69,
	"num*": 0x6A, "num+": 0x6B, "num-": 0x6D, "num.": 0x6E, "num/": 0x6F,
}

// Extended keys need KEYEVENTF_EXTENDEDKEY on Windows.
var extendedVK = map[uint16]bool{
	0x21: true, 0x22: true, 0x23: true, 0x24: true, 0x25: true, 0x26: true, 0x27: true, 0x28: true,
	0x2D: true, 0x2E: true, 0x5B: true, 0x5C: true, 0x5D: true, 0x6F: true, 0x2C: true,
	0xA6: true, 0xA7: true, 0xA8: true, 0xAD: true, 0xAE: true, 0xAF: true,
	0xB0: true, 0xB1: true, 0xB2: true, 0xB3: true,
}

func isModifier(vk uint16) bool { return vk == 0x10 || vk == 0x11 || vk == 0x12 || vk == 0x5B }

// ParseKeys turns "Ctrl+Shift+Z" into virtual key codes.
func ParseKeys(s string) (KeyCombo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty shortcut")
	}
	var mods, keys []uint16
	parts := strings.Split(s, "+")
	// "Ctrl++" means Ctrl and the plus key.
	if strings.HasSuffix(s, "++") {
		parts = append(strings.Split(strings.TrimSuffix(s, "++"), "+"), "plus")
	}
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		vk, ok := vkNames[p]
		if !ok {
			switch {
			case len(p) == 1 && p[0] >= 'a' && p[0] <= 'z':
				vk = uint16(p[0]-'a') + 0x41
			case len(p) == 1 && p[0] >= '0' && p[0] <= '9':
				vk = uint16(p[0]-'0') + 0x30
			case len(p) >= 2 && p[0] == 'f':
				var n int
				if _, err := fmt.Sscanf(p[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
					vk = uint16(0x70 + n - 1)
				} else {
					return nil, fmt.Errorf("unknown key %q", p)
				}
			default:
				return nil, fmt.Errorf("unknown key %q", p)
			}
		}
		if isModifier(vk) {
			mods = append(mods, vk)
		} else {
			keys = append(keys, vk)
		}
	}
	if len(mods)+len(keys) == 0 {
		return nil, fmt.Errorf("empty shortcut")
	}
	return append(mods, keys...), nil
}

func pressCombo(a Actuator, k KeyCombo, down bool) {
	if down {
		for _, vk := range k {
			a.Key(vk, true)
		}
	} else {
		for i := len(k) - 1; i >= 0; i-- {
			a.Key(k[i], false)
		}
	}
}

func tapCombo(a Actuator, k KeyCombo) {
	pressCombo(a, k, true)
	pressCombo(a, k, false)
}

func tapKeys(a Actuator, s string) {
	if k, err := ParseKeys(s); err == nil {
		tapCombo(a, k)
	} else {
		logf("action: %v", err)
	}
}

// Built-in actions that are just shortcuts.
var builtinKeys = map[string]string{
	"taskview":     "Win+Tab",
	"showdesktop":  "Win+D",
	"desktopPrev":  "Ctrl+Win+Left",
	"desktopNext":  "Ctrl+Win+Right",
	"alttab":       "Alt+Tab",
	"copy":         "Ctrl+C",
	"cut":          "Ctrl+X",
	"paste":        "Ctrl+V",
	"undo":         "Ctrl+Z",
	"redo":         "Ctrl+Y",
	"selectall":    "Ctrl+A",
	"save":         "Ctrl+S",
	"find":         "Ctrl+F",
	"screenshot":   "Win+Shift+S",
	"newtab":       "Ctrl+T",
	"closetab":     "Ctrl+W",
	"reopentab":    "Ctrl+Shift+T",
	"nexttab":      "Ctrl+Tab",
	"prevtab":      "Ctrl+Shift+Tab",
	"browserback":  "BrowserBack",
	"browserfwd":   "BrowserForward",
	"refresh":      "F5",
	"zoomin":       "Ctrl+Plus",
	"zoomout":      "Ctrl+Minus",
	"zoomreset":    "Ctrl+0",
	"maximize":     "Win+Up",
	"minimize":     "Win+Down",
	"snapleft":     "Win+Left",
	"snapright":    "Win+Right",
	"closewindow":  "Alt+F4",
	"lock":         "Win+L",
	"emoji":        "Win+.",
	"clipboard":    "Win+V",
	"explorer":     "Win+E",
	"actioncenter": "Win+A",
	"search":       "Win+S",
	"run":          "Win+R",
}

var mediaKeys = map[string]string{
	"playpause": "MediaPlayPause", "next": "MediaNext", "prev": "MediaPrev",
	"volup": "VolumeUp", "voldown": "VolumeDown", "mute": "VolumeMute", "stop": "MediaStop",
}

// heldAction remembers what to release when the button goes up.
type heldAction struct {
	release func()
}

// DeviceOps lets actions reach the mouse itself (SmartShift, DPI).
type DeviceOps interface {
	ToggleSmartShift()
	CycleDPI()
	SetTempDPI(dpi int) // 0 = restore
	PrecisionDPI() int
}

// Press starts an action. It returns a release func for hold-style actions.
func runAction(act Action, a Actuator, dev DeviceOps) func() {
	switch act.Type {
	case "keys":
		k, err := ParseKeys(act.Value)
		if err != nil {
			logf("action: %v", err)
			return nil
		}
		pressCombo(a, k, true)
		return func() { pressCombo(a, k, false) }
	case "mouse":
		btn := act.Value
		if btn == "double" {
			a.MouseButton("left", true)
			a.MouseButton("left", false)
			a.MouseButton("left", true)
			a.MouseButton("left", false)
			return nil
		}
		a.MouseButton(btn, true)
		return func() { a.MouseButton(btn, false) }
	case "media":
		if k, ok := mediaKeys[act.Value]; ok {
			tapKeys(a, k)
		}
	case "builtin":
		switch act.Value {
		case "smartshift":
			if dev != nil {
				dev.ToggleSmartShift()
			}
		case "dpicycle":
			if dev != nil {
				dev.CycleDPI()
			}
		case "precision":
			if dev != nil {
				dev.SetTempDPI(dev.PrecisionDPI())
				return func() { dev.SetTempDPI(0) }
			}
		default:
			if k, ok := builtinKeys[act.Value]; ok {
				tapKeys(a, k)
			} else {
				logf("action: unknown builtin %q", act.Value)
			}
		}
	case "run":
		if strings.TrimSpace(act.Value) != "" {
			a.Run(strings.TrimSpace(act.Value))
		}
	}
	return nil
}

// thumbStep performs one notch of a thumb wheel action. dir is +1 or -1.
// Returns false when the action is "default" (native behaviour wanted).
func thumbStep(w WheelAction, dir int, a Actuator) {
	pick := func(neg, pos string) {
		if dir < 0 {
			tapKeys(a, neg)
		} else {
			tapKeys(a, pos)
		}
	}
	switch w.Type {
	case "zoom":
		a.Key(0x11, true)
		a.Wheel(120*dir, false)
		a.Key(0x11, false)
	case "volume":
		pick("VolumeDown", "VolumeUp")
	case "tabs":
		pick("Ctrl+Shift+Tab", "Ctrl+Tab")
	case "brush":
		pick("[", "]")
	case "undoredo":
		pick("Ctrl+Z", "Ctrl+Y")
	case "arrows":
		pick("Left", "Right")
	case "desktops":
		pick("Ctrl+Win+Left", "Ctrl+Win+Right")
	case "custom":
		if dir < 0 && w.Left != "" {
			tapKeys(a, w.Left)
		} else if dir > 0 && w.Right != "" {
			tapKeys(a, w.Right)
		}
	default: // "default" / "hscroll": horizontal scrolling
		a.Wheel(120*dir, true)
	}
}
