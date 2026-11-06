package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const appVersion = "1.0.0"

//go:embed ui/index.html
var indexHTML string

func init() { runtime.LockOSThread() }

type App struct {
	engine  *Engine
	scanner *Scanner
	tray    *Tray
	dataDir string

	uiMu      sync.Mutex
	ui        webview2.WebView
	pushQueued bool
	quitting  bool
}

func main() {
	tray := false
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, "--tray") || strings.EqualFold(a, "/tray") {
			tray = true
		}
	}
	// One copy only: a second launch just opens the existing window.
	_, err := windows.CreateMutex(nil, false, u16("Local\\Mishk.SingleInstance"))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("MishkTray"))), 0); h != 0 {
			pPostMessageW.Call(h, wmShowUI, 0, 0)
		}
		return
	}

	dir := appDataDir()
	initLog(dir)
	logf("Mishk %s starting (%s)", appVersion, strings.Join(os.Args, " "))
	store := NewConfigStore(dir)
	_, statErr := os.Stat(filepath.Join(dir, "config.json"))
	firstRun := statErr != nil
	cfg := store.Load()
	if firstRun {
		_ = store.Save(cfg)
	}

	app := &App{dataDir: dir}
	app.engine = NewEngine(cfg, store, winActuator{}, foregroundExe)
	app.scanner = NewScanner(app.engine)
	t, err := NewTray(app)
	if err != nil {
		messageBox("Mishk", "Could not start: "+err.Error(), 0x10)
		return
	}
	app.tray = t
	app.engine.OnChange = app.onEngineChange
	app.engine.OnNotify = func(title, body string) { t.Notify(title, body) }
	app.engine.Start()
	go app.scanner.Run()

	if firstRun {
		_ = setAutostart(true)
	}
	if !tray && !(cfg.StartMinimized && !firstRun) {
		t.wantUI = true
	}
	if optionsPlusRunning() {
		logf("Logi Options+ is running - it may fight over the mouse")
	}
	app.loop()
	logf("Mishk exiting")
}

// loop is the main message loop. The settings window runs its own nested
// loop while open; when it closes we come back here.
func (a *App) loop() {
	var m msgT
	for {
		if a.tray.wantUI && !a.quitting {
			a.tray.wantUI = false
			a.openUI()
			if a.quitting {
				break
			}
			continue
		}
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	a.shutdown()
}

func (a *App) shutdown() {
	a.tray.Remove()
	a.engine.Stop()
	a.scanner.CloseAll()
}

func (a *App) Quit() {
	a.quitting = true
	a.uiMu.Lock()
	ui := a.ui
	a.uiMu.Unlock()
	if ui != nil {
		ui.Destroy() // its loop ends, then loop() sees quitting
	} else {
		pPostQuitMessage.Call(0)
	}
}

func (a *App) onEngineChange() {
	pPostMessageW.Call(a.tray.hwnd, wmRefresh, 0, 0)
	a.pushState()
}

// pushState sends fresh state to the window, at most ~10 times a second.
func (a *App) pushState() {
	a.uiMu.Lock()
	if a.ui == nil || a.pushQueued {
		a.uiMu.Unlock()
		return
	}
	a.pushQueued = true
	a.uiMu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		a.uiMu.Lock()
		ui := a.ui
		a.pushQueued = false
		a.uiMu.Unlock()
		if ui == nil {
			return
		}
		b, _ := json.Marshal(a.state())
		ui.Dispatch(func() {
			a.uiMu.Lock()
			still := a.ui == ui
			a.uiMu.Unlock()
			if still {
				ui.Eval("window.__mishkPush && window.__mishkPush(" + string(b) + ")")
			}
		})
	})
}

type stateView struct {
	Version     string       `json:"version"`
	Devices     []DeviceView `json:"devices"`
	Config      *Config      `json:"config"`
	OptionsPlus bool         `json:"optionsPlus"`
	Autostart   bool         `json:"autostart"`
	DataDir     string       `json:"dataDir"`
	Pairing     *PairingView   `json:"pairing"`
	Receivers   []ReceiverView `json:"receivers"`
}

var optionsPlusCache struct {
	at  time.Time
	val bool
}

func (a *App) state() stateView {
	if time.Since(optionsPlusCache.at) > 5*time.Second {
		optionsPlusCache.val = optionsPlusRunning()
		optionsPlusCache.at = time.Now()
	}
	return stateView{
		Version:     appVersion,
		Devices:     a.engine.Devices(),
		Config:      a.engine.Config(),
		OptionsPlus: optionsPlusCache.val,
		Autostart:   autostartEnabled(),
		DataDir:     a.dataDir,
		Pairing:     a.engine.Pairing(),
		Receivers:   a.engine.Receivers(),
	}
}
