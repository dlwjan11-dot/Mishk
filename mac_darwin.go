package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework IOKit -framework ApplicationServices -mmacosx-version-min=11.0
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const appVersion = "1.1.0"

//go:embed ui/index.html
var indexHTML string

func init() { runtime.LockOSThread() }

type macApp struct {
	engine  *Engine
	dataDir string
	shown   atomic.Bool
	pushMu  sync.Mutex
	pushing bool
}

var mac *macApp

func main() {
	tray := false
	for _, a := range os.Args[1:] {
		if a == "--tray" {
			tray = true
		}
	}
	dir := appDataDir()
	initLog(dir)
	logf("Mishk %s starting on macOS (%s)", appVersion, strings.Join(os.Args, " "))
	store := NewConfigStore(dir)
	_, statErr := os.Stat(filepath.Join(dir, "config.json"))
	firstRun := statErr != nil
	cfg := store.Load()
	if firstRun {
		_ = store.Save(cfg)
	}
	mac = &macApp{dataDir: dir}
	mac.engine = NewEngine(cfg, store, macActuator{}, macFrontApp)
	mac.engine.OnChange = mac.onChange
	mac.engine.OnNotify = macNotify
	mac.engine.EnableGeneric(nil, genericName(cfg.Lang))
	mac.engine.Start()
	if firstRun {
		_ = setLoginItem(true)
	}
	show := !tray && !(cfg.StartMinimized && !firstRun)
	html := C.CString(indexHTML)
	C.macRun(html, b2i(show)) // never returns
}

func b2i(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

//export goAppReady
func goAppReady() {
	C.macStartHID()
	mac.updateTap()
	mac.updateStatus()
}

//export goWake
func goWake() {
	logf("system woke from sleep")
	mac.engine.postAfter(3*time.Second, func() { mac.engine.Refresh() })
}

//export goWindowShown
func goWindowShown(on C.int) {
	mac.shown.Store(on != 0)
	if on != 0 {
		C.macSetDark(b2i(mac.engine.Config().Theme != "light"))
	}
}

//export goMenu
func goMenu(item C.int) {
	switch item {
	case 2:
		c := mac.engine.Config()
		c.Paused = !c.Paused
		_ = mac.engine.SetConfig(c)
		mac.updateTap()
		mac.pushState()
	case 3:
		mac.quit()
	}
}

func (a *macApp) quit() {
	a.engine.Stop()
	C.macQuit()
}

func (a *macApp) onChange() {
	a.updateStatus()
	a.pushState()
}

func (a *macApp) updateStatus() {
	name, bat, online := a.engine.Summary()
	ar := a.engine.Config().Lang == "ar"
	tr := func(arS, en string) string {
		if ar {
			return arS
		}
		return en
	}
	line := tr("ما في ماوس متصل", "No mouse connected")
	if name != "" {
		switch {
		case !online:
			line = name + tr(" - نايم أو غير متصل", " - asleep or disconnected")
		case bat >= 0:
			line = fmt.Sprintf("%s - %d%%", name, bat)
		default:
			line = name
		}
	}
	cs := func(s string) *C.char { return C.CString(s) }
	t, l, o, p, q := cs("Mishk - "+line), cs(line), cs(tr("فتح الإعدادات", "Open settings")), cs(tr("إيقاف مؤقت للأزرار", "Pause button remapping")), cs(tr("خروج", "Quit"))
	C.macSetStatus(t, l, b2i(a.engine.Config().Paused), o, p, q)
	for _, x := range []*C.char{t, l, o, p, q} {
		C.free(unsafe.Pointer(x))
	}
}

func (a *macApp) pushState() {
	if !a.shown.Load() {
		return
	}
	a.pushMu.Lock()
	if a.pushing {
		a.pushMu.Unlock()
		return
	}
	a.pushing = true
	a.pushMu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		a.pushMu.Lock()
		a.pushing = false
		a.pushMu.Unlock()
		b, _ := json.Marshal(a.state())
		js := C.CString("window.__mishkPush && window.__mishkPush(" + string(b) + ")")
		C.macEval(js)
		C.free(unsafe.Pointer(js))
	})
}

// updateTap turns the event tap for non-Logitech mice on or off.
func (a *macApp) updateTap() {
	need := a.engine.GenericNeeded()
	if need && C.macAccessibilityTrusted(1) == 0 {
		logf("Accessibility permission is needed for other mice")
		return
	}
	if C.macSetTap(b2i(need)) == 0 {
		logf("event tap could not start")
	}
}

type macState struct {
	Version       string         `json:"version"`
	Platform      string         `json:"platform"`
	Devices       []DeviceView   `json:"devices"`
	Config        *Config        `json:"config"`
	OptionsPlus   bool           `json:"optionsPlus"`
	Autostart     bool           `json:"autostart"`
	DataDir       string         `json:"dataDir"`
	Pairing       *PairingView   `json:"pairing"`
	Receivers     []ReceiverView `json:"receivers"`
	Accessibility bool           `json:"accessibility"`
}

var optPlusCache struct {
	at  time.Time
	val bool
}

func (a *macApp) state() macState {
	if time.Since(optPlusCache.at) > 5*time.Second {
		optPlusCache.val = optionsPlusRunningMac()
		optPlusCache.at = time.Now()
	}
	return macState{
		Version: appVersion, Platform: "mac", Devices: a.engine.Devices(), Config: a.engine.Config(),
		OptionsPlus: optPlusCache.val, Autostart: loginItemEnabled(), DataDir: a.dataDir,
		Pairing: a.engine.Pairing(), Receivers: a.engine.Receivers(), Accessibility: C.macAccessibilityTrusted(0) != 0,
	}
}

//export goBridgeCall
func goBridgeCall(ccmd, carg *C.char) *C.char {
	cmd, arg := C.GoString(ccmd), json.RawMessage(C.GoString(carg))
	res, err := mac.handle(cmd, arg)
	var out []byte
	if err != nil {
		out, _ = json.Marshal(map[string]any{"error": err.Error()})
	} else {
		out, _ = json.Marshal(map[string]any{"result": res})
	}
	return C.CString(string(out)) // freed by the Objective-C side
}

func (a *macApp) handle(cmd string, arg json.RawMessage) (any, error) {
	switch cmd {
	case "state":
		return a.state(), nil
	case "setConfig", "importConfig":
		var c Config
		if cmd == "importConfig" {
			var s string
			if err := json.Unmarshal(arg, &s); err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(s), &c); err != nil || c.Buttons == nil {
				return nil, errors.New("not a Mishk settings file")
			}
		} else if err := json.Unmarshal(arg, &c); err != nil {
			return nil, err
		}
		if err := a.engine.SetConfig(&c); err != nil {
			return nil, err
		}
		C.macSetDark(b2i(c.Theme != "light"))
		a.updateTap()
		a.updateStatus()
		return a.state(), nil
	case "switchHost":
		var p struct {
			Key  string `json:"key"`
			Host int    `json:"host"`
		}
		if err := json.Unmarshal(arg, &p); err != nil {
			return nil, err
		}
		a.engine.SwitchHost(p.Key, p.Host)
		return true, nil
	case "refresh":
		a.engine.Refresh()
		return true, nil
	case "log":
		return logText(), nil
	case "openDataFolder":
		_ = exec.Command("open", a.dataDir).Start()
		return true, nil
	case "apps":
		var apps []appInfoMac
		_ = json.Unmarshal([]byte(cstr(C.macRunningApps())), &apps)
		return apps, nil
	case "setAutostart":
		var on bool
		_ = json.Unmarshal(arg, &on)
		return loginItemEnabled(), setLoginItem(on)
	case "quitOptionsPlus":
		_ = exec.Command("pkill", "-if", "logioptionsplus").Run()
		optPlusCache.at = time.Time{}
		return 1, nil
	case "exportConfig":
		home, _ := os.UserHomeDir()
		p := filepath.Join(home, "Documents", fmt.Sprintf("Mishk-settings-%s.json", time.Now().Format("2006-01-02")))
		b, _ := json.MarshalIndent(a.engine.Config(), "", "  ")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, err
		}
		_ = exec.Command("open", "-R", p).Start()
		return p, nil
	case "photo":
		c := a.engine.Config()
		if c.MousePhoto == "" {
			return "", nil
		}
		b, err := os.ReadFile(filepath.Join(a.dataDir, filepath.Base(c.MousePhoto)))
		if err != nil {
			return "", nil
		}
		mime := "image/jpeg"
		if strings.HasSuffix(c.MousePhoto, ".png") {
			mime = "image/png"
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b), nil
	case "setPhoto":
		var u string
		if err := json.Unmarshal(arg, &u); err != nil {
			return nil, err
		}
		c := a.engine.Config()
		if u == "" {
			c.MousePhoto, c.PhotoSpots, c.PhotoScale, c.PhotoShift = "", nil, 1, [2]float64{}
		} else {
			i := strings.Index(u, ";base64,")
			if !strings.HasPrefix(u, "data:image/") || i < 0 {
				return nil, errors.New("not an image")
			}
			b, err := base64.StdEncoding.DecodeString(u[i+8:])
			if err != nil {
				return nil, err
			}
			name := "mouse-photo.jpg"
			if strings.HasPrefix(u, "data:image/png") {
				name = "mouse-photo.png"
			}
			_ = os.Remove(filepath.Join(a.dataDir, "mouse-photo.jpg"))
			_ = os.Remove(filepath.Join(a.dataDir, "mouse-photo.png"))
			if err := os.WriteFile(filepath.Join(a.dataDir, name), b, 0o644); err != nil {
				return nil, err
			}
			c.MousePhoto = name
		}
		if err := a.engine.SetConfig(c); err != nil {
			return nil, err
		}
		return a.state(), nil
	case "startPairing":
		var link string
		_ = json.Unmarshal(arg, &link)
		a.engine.StartPairing(link)
		return true, nil
	case "cancelPairing":
		a.engine.CancelPairing()
		return true, nil
	case "clearPairing":
		a.engine.ClearPairing()
		return true, nil
	case "unpair":
		var key string
		_ = json.Unmarshal(arg, &key)
		a.engine.Unpair(key)
		return true, nil
	case "askAccessibility":
		C.macAccessibilityTrusted(1)
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
		return true, nil
	case "openUrl":
		var u string
		_ = json.Unmarshal(arg, &u)
		if strings.HasPrefix(u, "https://") {
			_ = exec.Command("open", u).Start()
		}
		return true, nil
	case "quit":
		go func() {
			time.Sleep(50 * time.Millisecond)
			a.quit()
		}()
		return true, nil
	}
	return nil, fmt.Errorf("unknown command %q", cmd)
}

type appInfoMac struct {
	Exe   string `json:"exe"`
	Title string `json:"title"`
}

func cstr(p *C.char) string {
	s := C.GoString(p)
	C.free(unsafe.Pointer(p))
	return s
}

func macFrontApp() string { return strings.ToLower(cstr(C.macFrontApp())) }

func macNotify(title, body string) {
	q := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
	_ = exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s"`, q(body), q(title))).Start()
}

func optionsPlusRunningMac() bool {
	return exec.Command("pgrep", "-if", "logioptionsplus").Run() == nil
}

// ---------- login item (LaunchAgent) ----------

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.abdulmedia.mishk.plist")
}

func loginItemEnabled() bool {
	_, err := os.Stat(launchAgentPath())
	return err == nil
}

func setLoginItem(on bool) error {
	p := launchAgentPath()
	if !on {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.abdulmedia.mishk</string>
  <key>ProgramArguments</key><array><string>%s</string><string>--tray</string></array>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Interactive</string>
</dict></plist>
`, exe)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return os.WriteFile(p, []byte(plist), 0o644)
}

// ---------- HID++ links ----------

type macLink struct {
	dev    uintptr
	outLen int
	closed atomic.Bool
}

func (l *macLink) HasShort() bool { return true }

func (l *macLink) Send(rep []byte) error {
	if l.closed.Load() {
		return ErrClosed
	}
	n := 7
	if rep[0] != reportShort {
		n = 20
		if l.outLen > 20 && rep[0] == reportVLong {
			n = l.outLen
		}
	}
	buf := make([]byte, n)
	copy(buf, rep)
	if r := C.macHIDWrite(C.uintptr_t(l.dev), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(n)); r != 0 {
		return fmt.Errorf("IOHIDDeviceSetReport 0x%X", uint32(r))
	}
	return nil
}

func (l *macLink) Close() { l.closed.Store(true) }

var macLinks struct {
	mu sync.Mutex
	m  map[uintptr]*macLinkEntry
}

type macLinkEntry struct {
	id   string
	link *macLink
	ch   *Channel
}

//export goHIDAdded
func goHIDAdded(dev C.uintptr_t, pid, page, outLen C.int, product *C.char) {
	d := uintptr(dev)
	id := fmt.Sprintf("mac-%x", d)
	kind := "direct"
	name := C.GoString(product)
	if isReceiverPID(uint16(pid)) {
		kind = "receiver"
		if uint16(pid) == 0xC548 {
			name = "Logi Bolt"
		} else if !strings.Contains(strings.ToLower(name), "unifying") {
			name = "Unifying " + name
		}
	}
	link := &macLink{dev: d, outLen: int(outLen)}
	logf("hid: found %s pid=%04X page=%04X out=%d (%s)", id, int(pid), int(page), int(outLen), name)
	ch := mac.engine.AddLink(id, kind, name, link)
	macLinks.mu.Lock()
	if macLinks.m == nil {
		macLinks.m = map[uintptr]*macLinkEntry{}
	}
	macLinks.m[d] = &macLinkEntry{id: id, link: link, ch: ch}
	macLinks.mu.Unlock()
}

//export goHIDRemoved
func goHIDRemoved(dev C.uintptr_t) {
	d := uintptr(dev)
	macLinks.mu.Lock()
	e := macLinks.m[d]
	delete(macLinks.m, d)
	macLinks.mu.Unlock()
	if e == nil {
		return
	}
	e.link.closed.Store(true)
	mac.engine.RemoveLink(e.id)
	go func() {
		time.Sleep(2 * time.Second)
		C.macHIDRelease(dev)
	}()
}

//export goHIDInput
func goHIDInput(dev C.uintptr_t, data *C.uint8_t, n C.int) {
	macLinks.mu.Lock()
	e := macLinks.m[uintptr(dev)]
	macLinks.mu.Unlock()
	if e == nil || n <= 0 {
		return
	}
	e.ch.Deliver(C.GoBytes(unsafe.Pointer(data), n))
}

func isReceiverPID(pid uint16) bool { return pid&0xFF00 == 0xC500 }

// ---------- any mouse (event tap) ----------

var tapState struct {
	taken   map[string]bool
	gesture string
}

//export goTapButton
func goTapButton(button, down C.int) C.int {
	key := map[C.int]string{2: "middle", 3: "back", 4: "forward"}[button]
	if key == "" {
		return 0
	}
	if tapState.taken == nil {
		tapState.taken = map[string]bool{}
	}
	if down != 0 {
		take, gestures := mac.engine.GenericWants(key)
		if !take {
			return 0
		}
		tapState.taken[key] = true
		if gestures {
			tapState.gesture = key
		}
		mac.engine.GenericButton(key, true)
		return 1
	}
	if tapState.taken[key] {
		delete(tapState.taken, key)
		if tapState.gesture == key {
			tapState.gesture = ""
		}
		mac.engine.GenericButton(key, false)
		return 1
	}
	return 0
}

//export goTapMove
func goTapMove(dx, dy C.int) {
	if tapState.gesture != "" && (dx != 0 || dy != 0) {
		mac.engine.GenericMove(int(dx), int(dy))
	}
}
