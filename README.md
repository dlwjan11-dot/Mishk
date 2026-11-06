<div align="center">

<img src="res/icon-1024.png" width="96" alt="">

# Mishk

**A small, fast replacement for Logi Options+ on Windows**

For Logitech MX mice, made for the MX Master 3S on a Logi Bolt receiver or Bluetooth.

English · [العربية](README.ar.md)

[**Download Mishk for Windows**](https://github.com/dlwjan11-dot/Mishk/raw/main/download/Mishk-Windows.zip)

</div>

![Mishk buttons page](docs/buttons-en.png)

## Why

Logi Options+ is heavy, crashes, and sometimes disappears from the tray. Mishk is one 3.6 MB file with no installer and no account. It opens instantly, and when you close the window it drops back to the tray and frees its memory.

Mishk talks to the mouse directly over Logitech's HID++ protocol. Buttons are reported by the mouse itself, so there's no Windows mouse hook that can time out and leave your buttons dead.

## What it does

**Buttons**
- Remap the gesture, back, forward, middle and wheel-mode buttons
- Gesture button: a press, or a swipe up, down, left or right, each with its own action
- Over 60 actions: Windows (task view, desktops, snapping), editing, browser, media, mouse clicks, keyboard shortcuts, open an app or a link
- Hold-style shortcuts: the keys stay held while you hold the button, e.g. Space for Photoshop's Hand tool
- Precision mode: hold a button to slow the pointer down, let go to return to normal speed

**Per-app profiles**
- Different buttons and thumb wheel for each app (Photoshop, Premiere, Chrome…), switched automatically when the app is in front
- Pick from the apps open right now, or type the file name

**Thumb wheel**
- Horizontal scroll, zoom, brush size, volume, tabs, undo/redo, timeline arrows, desktops, or two custom shortcuts

**Pointer and scrolling**
- Pointer speed (DPI) with presets, and a button action to cycle through them
- SmartShift, always ratchet or always free-spin, sensitivity and ratchet force
- Smooth scrolling, reversed scroll for the main wheel and the thumb wheel

**Pairing and the device**
- First-time pairing with a Logi Bolt receiver, including the left/right click passkey, and unpairing
- Battery in the tray with a low battery alert
- Easy-Switch channels
- Settings are put back automatically when the mouse wakes up or reconnects

**The app**
- Arabic and English interface, dark and light
- Use your own photo of the mouse: the background is removed and the dots are placed on the buttons automatically
- Starts with Windows, export and import settings, a log for troubleshooting

| Pointer & scroll | Apps | Pairing |
|---|---|---|
| ![](docs/scroll-en.png) | ![](docs/apps-en.png) | ![](docs/pairing-en.png) |

## Getting started

1. Quit Logi Options+, and turn off its startup. Both apps fight over the mouse. Mishk warns you if Options+ is running and can close it.
2. [Download Mishk-Windows.zip](https://github.com/dlwjan11-dot/Mishk/raw/main/download/Mishk-Windows.zip), right-click it and choose **Extract All**, then run `Mishk.exe`.
3. Windows SmartScreen may warn you because the app isn't signed: click **More info**, then **Run anyway**.

Mishk adds itself to the tray and starts with Windows. Settings are saved in `%APPDATA%\Mishk`.

Needs Windows 10 or 11 with the Microsoft Edge WebView2 Runtime (already part of Windows 11).

## Limitations

- Shortcuts don't reach apps running as Administrator unless Mishk runs as Administrator too.
- Tested against a simulated MX Master 3S and Bolt receiver. Real-hardware reports are welcome: open **Device & settings → Show log** and attach it to an issue.

## Building

Needs Go 1.22+, Python 3 and mingw `windres`. Dependencies are vendored, so it builds offline:

```sh
./build.sh        # writes dist/Mishk.exe
go test ./...     # runs the protocol engine against a simulated MX Master 3S
```

## Credits

- HID++ details cross-checked against [Solaar](https://github.com/pwr-Solaar/Solaar) and [Mouser](https://github.com/TomBadash/Mouser) (MIT)
- Mouse image by Abdulrahman (Abdulmedia)
- [go-webview2](https://github.com/jchv/go-webview2) (MIT)
- IBM Plex Sans Arabic, SIL Open Font License ([res/FONT-LICENSE-OFL.txt](res/FONT-LICENSE-OFL.txt))

Not affiliated with Logitech. Logitech, Logi, MX Master and Logi Bolt are trademarks of Logitech.

## License

[MIT](LICENSE)
