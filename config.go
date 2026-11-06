package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Action is what a button (or a gesture direction) does.
//
//	type: default | none | keys | mouse | media | builtin | run | gestures | inherit
type Action struct {
	Type     string    `json:"type"`
	Value    string    `json:"value,omitempty"`
	Gestures *Gestures `json:"gestures,omitempty"`
}

type Gestures struct {
	Click Action `json:"click"`
	Up    Action `json:"up"`
	Down  Action `json:"down"`
	Left  Action `json:"left"`
	Right Action `json:"right"`
}

// WheelAction is what the thumb wheel does.
//
//	type: default | zoom | volume | tabs | brush | undoredo | arrows | desktops | hscroll | custom | inherit
type WheelAction struct {
	Type  string `json:"type"`
	Left  string `json:"left,omitempty"`  // custom: keys when rolled toward the user / left
	Right string `json:"right,omitempty"` // custom: keys when rolled the other way
}

type Profile struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Exe     string            `json:"exe"` // lower-case file name, e.g. photoshop.exe
	Buttons map[string]Action `json:"buttons"`
	Thumb   WheelAction       `json:"thumb"`
}

type SmartShift struct {
	Mode        string `json:"mode"`        // smart | ratchet | free
	Sensitivity int    `json:"sensitivity"` // 1..50
	Torque      int    `json:"torque"`      // 1..100 (0 = leave as is)
}

type Config struct {
	Lang             string            `json:"lang"` // ar | en
	Buttons          map[string]Action `json:"buttons"`
	Thumb            WheelAction       `json:"thumb"`
	ThumbSensitivity int               `json:"thumbSensitivity"` // 1..10
	DPI              int               `json:"dpi"`              // 0 = leave as is
	PrecisionDPI     int               `json:"precisionDpi"`
	DPIPresets       []int             `json:"dpiPresets"`
	SmartShift       *SmartShift       `json:"smartShift,omitempty"` // nil = leave as is
	SmoothScroll     *bool             `json:"smoothScroll,omitempty"`
	InvertScroll     *bool             `json:"invertScroll,omitempty"`
	InvertThumb      *bool             `json:"invertThumb,omitempty"`
	GestureDistance  int               `json:"gestureDistance"` // raw counts before a move counts as a swipe
	Profiles         []Profile         `json:"profiles"`
	Paused           bool              `json:"paused"`
	LowBatteryAlert  bool              `json:"lowBatteryAlert"`
	StartMinimized   bool              `json:"startMinimized"`
	Theme            string            `json:"theme"` // dark | light | system

	// The user's own photo of their mouse, shown instead of the drawing.
	MousePhoto string                `json:"mousePhoto,omitempty"` // file name in the data folder
	PhotoScale float64               `json:"photoScale,omitempty"`
	PhotoShift [2]float64            `json:"photoShift"` // pan offset in diagram units
	PhotoSpots map[string][2]float64 `json:"photoSpots,omitempty"` // button -> x,y in the 760x560 diagram
}

// Button keys, in the order shown in the UI.
var buttonKeys = []string{"gesture", "back", "forward", "middle", "top"}

var buttonCID = map[string]uint16{
	"middle":  0x0052,
	"back":    0x0053,
	"forward": 0x0056,
	"gesture": 0x00C3,
	"top":     0x00C4,
}

func gesturesVirtualDesktops() *Gestures {
	return &Gestures{
		Click: Action{Type: "builtin", Value: "taskview"},
		Up:    Action{Type: "builtin", Value: "taskview"},
		Down:  Action{Type: "builtin", Value: "showdesktop"},
		Left:  Action{Type: "builtin", Value: "desktopPrev"},
		Right: Action{Type: "builtin", Value: "desktopNext"},
	}
}

func DefaultConfig() *Config {
	return &Config{
		Lang: "en",
		Buttons: map[string]Action{
			"gesture": {Type: "gestures", Gestures: gesturesVirtualDesktops()},
			"back":    {Type: "default"},
			"forward": {Type: "default"},
			"middle":  {Type: "default"},
			"top":     {Type: "default"},
		},
		Thumb:            WheelAction{Type: "default"},
		ThumbSensitivity: 5,
		PrecisionDPI:     400,
		DPIPresets:       []int{800, 1600, 3200},
		GestureDistance:  60,
		LowBatteryAlert:  true,
		Theme:            "dark",
		Profiles:         []Profile{},
	}
}

func (c *Config) normalize() {
	d := DefaultConfig()
	if c.Lang != "ar" {
		c.Lang = "en"
	}
	if c.Buttons == nil {
		c.Buttons = map[string]Action{}
	}
	for _, k := range buttonKeys {
		if _, ok := c.Buttons[k]; !ok {
			c.Buttons[k] = d.Buttons[k]
		}
	}
	for k, a := range c.Buttons {
		if a.Type == "" || a.Type == "inherit" {
			a.Type = "default"
		}
		if a.Type == "gestures" && a.Gestures == nil {
			a.Gestures = gesturesVirtualDesktops()
		}
		c.Buttons[k] = a
	}
	if c.Thumb.Type == "" || c.Thumb.Type == "inherit" {
		c.Thumb.Type = "default"
	}
	if c.ThumbSensitivity < 1 || c.ThumbSensitivity > 10 {
		c.ThumbSensitivity = 5
	}
	if c.PrecisionDPI < 100 {
		c.PrecisionDPI = 400
	}
	if len(c.DPIPresets) == 0 {
		c.DPIPresets = d.DPIPresets
	}
	if c.GestureDistance < 10 {
		c.GestureDistance = 60
	}
	if c.Profiles == nil {
		c.Profiles = []Profile{}
	}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		p.Exe = strings.ToLower(strings.TrimSpace(p.Exe))
		if p.Buttons == nil {
			p.Buttons = map[string]Action{}
		}
		if p.Thumb.Type == "" {
			p.Thumb.Type = "inherit"
		}
	}
	if c.Theme == "" {
		c.Theme = "dark"
	}
	if c.PhotoScale < 0.4 || c.PhotoScale > 2 {
		c.PhotoScale = 1
	}
}

func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	var n Config
	_ = json.Unmarshal(b, &n)
	n.normalize()
	return &n
}

// --- persistence ---

type ConfigStore struct {
	mu   sync.Mutex
	path string
}

func appDataDir() string {
	base, err := os.UserConfigDir() // %APPDATA% on Windows
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "Mishk")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func NewConfigStore(dir string) *ConfigStore {
	return &ConfigStore{path: filepath.Join(dir, "config.json")}
}

func (s *ConfigStore) Load() *Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return DefaultConfig()
	}
	c := DefaultConfig()
	if err := json.Unmarshal(b, c); err != nil {
		logf("config: bad json, using defaults: %v", err)
		_ = os.WriteFile(s.path+".broken", b, 0o644)
		return DefaultConfig()
	}
	c.normalize()
	return c
}

func (s *ConfigStore) Save(c *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
