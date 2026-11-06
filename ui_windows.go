package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

func (a *App) requestUI() {
	a.uiMu.Lock()
	ui := a.ui
	a.uiMu.Unlock()
	if ui != nil {
		h := uintptr(ui.Window())
		if r, _, _ := pIsIconic.Call(h); r != 0 {
			pShowWindow.Call(h, 9) // SW_RESTORE
		} else {
			pShowWindow.Call(h, 5) // SW_SHOW
		}
		pSetForegroundWindow.Call(h)
		return
	}
	a.tray.wantUI = true
}

func (a *App) openUI() {
	localApp, _ := os.UserCacheDir() // %LOCALAPPDATA%
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("MISHK_DEBUG") != "",
		AutoFocus: true,
		DataPath:  filepath.Join(localApp, "Mishk", "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title:  "Mishk",
			Width:  1120,
			Height: 760,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		ar := a.engine.Config().Lang == "ar"
		msg := "Mishk needs the Microsoft Edge WebView2 Runtime to show its window.\n\nIt is already part of Windows 11. Open the download page now?"
		if ar {
			msg = "البرنامج بيحتاج Microsoft Edge WebView2 Runtime لحتى يفتح النافذة.\n\nهو موجود أصلاً بويندوز 11. بدك تفتح صفحة التحميل؟"
		}
		if messageBox("Mishk", msg, 0x04|0x30) == 6 {
			shellOpen("https://developer.microsoft.com/microsoft-edge/webview2/", "")
		}
		logf("ui: WebView2 could not start")
		return
	}
	hwnd := uintptr(w.Window())
	setDarkTitleBar(hwnd, a.engine.Config().Theme != "light")
	w.SetSize(940, 640, webview2.HintMin)
	_ = w.Bind("mishkCall", func(cmd string, arg json.RawMessage) (any, error) {
		return a.handle(cmd, arg, hwnd)
	})
	w.SetHtml(indexHTML)

	a.uiMu.Lock()
	a.ui = w
	a.uiMu.Unlock()
	logf("ui: opened")
	w.Run()
	a.uiMu.Lock()
	a.ui = nil
	a.uiMu.Unlock()
	logf("ui: closed")
}

func (a *App) handle(cmd string, arg json.RawMessage, hwnd uintptr) (any, error) {
	switch cmd {
	case "state":
		return a.state(), nil
	case "setConfig":
		var c Config
		if err := json.Unmarshal(arg, &c); err != nil {
			return nil, err
		}
		if err := a.engine.SetConfig(&c); err != nil {
			return nil, err
		}
		setDarkTitleBar(hwnd, c.Theme != "light")
		pPostMessageW.Call(a.tray.hwnd, wmRefresh, 0, 0)
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
		a.scanner.Trigger()
		a.engine.Refresh()
		return true, nil
	case "log":
		return logText(), nil
	case "openDataFolder":
		shellOpen(a.dataDir, "")
		return true, nil
	case "apps":
		return runningApps(), nil
	case "setAutostart":
		var on bool
		_ = json.Unmarshal(arg, &on)
		return autostartEnabled(), setAutostart(on)
	case "quitOptionsPlus":
		n := quitOptionsPlus()
		optionsPlusCache.at = time.Time{}
		return n, nil
	case "exportConfig":
		docs, _ := os.UserHomeDir()
		dir := filepath.Join(docs, "Documents")
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			dir = docs
		}
		p := filepath.Join(dir, fmt.Sprintf("Mishk-settings-%s.json", time.Now().Format("2006-01-02")))
		b, _ := json.MarshalIndent(a.engine.Config(), "", "  ")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, err
		}
		shellOpen("explorer.exe", `/select,"`+p+`"`)
		return p, nil
	case "importConfig":
		var s string
		if err := json.Unmarshal(arg, &s); err != nil {
			return nil, err
		}
		var c Config
		if err := json.Unmarshal([]byte(s), &c); err != nil {
			return nil, errors.New("not a Mishk settings file")
		}
		if c.Buttons == nil {
			return nil, errors.New("not a Mishk settings file")
		}
		if err := a.engine.SetConfig(&c); err != nil {
			return nil, err
		}
		return a.state(), nil
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
		if strings.HasSuffix(strings.ToLower(c.MousePhoto), ".png") {
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
			c.MousePhoto = ""
			c.PhotoSpots = nil
			c.PhotoScale = 1
			c.PhotoShift = [2]float64{}
		} else {
			i := strings.Index(u, ";base64,")
			if !strings.HasPrefix(u, "data:image/") || i < 0 {
				return nil, errors.New("not an image")
			}
			b, err := base64.StdEncoding.DecodeString(u[i+8:])
			if err != nil {
				return nil, err
			}
			ext := ".jpg"
			if strings.HasPrefix(u, "data:image/png") {
				ext = ".png"
			}
			name := "mouse-photo" + ext
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
	case "openUrl":
		var u string
		_ = json.Unmarshal(arg, &u)
		if len(u) > 8 && u[:8] == "https://" {
			shellOpen(u, "")
		}
		return true, nil
	case "quit":
		go func() {
			time.Sleep(50 * time.Millisecond)
			pPostMessageW.Call(a.tray.hwnd, wmQuitApp, 0, 0)
		}()
		return true, nil
	}
	return nil, fmt.Errorf("unknown command %q", cmd)
}
