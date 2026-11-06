package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ---------- SendInput ----------

type input struct {
	Type uint32
	_    uint32
	Data [32]byte
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

const mishkMagic = 0x4D49534B // marks our own injected input

type winActuator struct{}

func sendInputs(in []input) {
	if len(in) == 0 {
		return
	}
	pSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

func (winActuator) Key(vk uint16, down bool) {
	var ki keybdInput
	ki.Vk = vk
	sc, _, _ := pMapVirtualKeyW.Call(uintptr(vk), 0)
	ki.Scan = uint16(sc)
	if extendedVK[vk] {
		ki.Flags |= 0x0001
	}
	if !down {
		ki.Flags |= 0x0002
	}
	ki.ExtraInfo = mishkMagic
	in := input{Type: 1}
	*(*keybdInput)(unsafe.Pointer(&in.Data[0])) = ki
	sendInputs([]input{in})
}

func mouse(flags uint32, data int32) {
	mi := mouseInput{Flags: flags, MouseData: uint32(data), ExtraInfo: mishkMagic}
	in := input{Type: 0}
	*(*mouseInput)(unsafe.Pointer(&in.Data[0])) = mi
	sendInputs([]input{in})
}

func (winActuator) MouseButton(btn string, down bool) {
	switch btn {
	case "left":
		mouse(map[bool]uint32{true: 0x0002, false: 0x0004}[down], 0)
	case "right":
		mouse(map[bool]uint32{true: 0x0008, false: 0x0010}[down], 0)
	case "middle":
		mouse(map[bool]uint32{true: 0x0020, false: 0x0040}[down], 0)
	case "back":
		mouse(map[bool]uint32{true: 0x0080, false: 0x0100}[down], 1)
	case "forward":
		mouse(map[bool]uint32{true: 0x0080, false: 0x0100}[down], 2)
	}
}

func (winActuator) Wheel(delta int, horizontal bool) {
	if horizontal {
		mouse(0x1000, int32(delta))
	} else {
		mouse(0x0800, int32(delta))
	}
}

func (winActuator) Run(target string) {
	t := os.ExpandEnv(strings.Trim(target, `"`))
	shellOpen(t, "")
}

// ---------- processes ----------

func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [1024]uint16
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

func foregroundExe() string {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	windows.GetWindowThreadProcessId(hwnd, &pid)
	return strings.ToLower(processName(pid))
}

type appInfo struct {
	Exe   string `json:"exe"`
	Title string `json:"title"`
}

// runningApps lists programs that have a visible window.
func runningApps() []appInfo {
	seen := map[string]appInfo{}
	self := strings.ToLower(filepath.Base(os.Args[0]))
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if owner, _, _ := pGetWindow.Call(hwnd, 4 /*GW_OWNER*/); owner != 0 {
			return 1
		}
		ex, _, _ := pGetWindowLongPtrW.Call(hwnd, ^uintptr(19) /* GWL_EXSTYLE = -20 */)
		if ex&0x00000080 != 0 { // WS_EX_TOOLWINDOW
			return 1
		}
		l, _, _ := pGetWindowTextLengthW.Call(hwnd)
		if l == 0 {
			return 1
		}
		buf := make([]uint16, l+1)
		pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), l+1)
		var pid uint32
		windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid)
		exe := strings.ToLower(processName(pid))
		if exe == "" || exe == self || exe == "textinputhost.exe" || exe == "explorer.exe" && windows.UTF16ToString(buf) == "Program Manager" {
			return 1
		}
		if _, ok := seen[exe]; !ok {
			seen[exe] = appInfo{Exe: exe, Title: windows.UTF16ToString(buf)}
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	var out []appInfo
	for _, a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Exe < out[j].Exe })
	return out
}

func findProcesses(prefixes ...string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []uint32
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				out = append(out, pe.ProcessID)
			}
		}
	}
	return out
}

var optionsPlusNames = []string{"logioptionsplus", "logioptions", "lghub_agent", "logi_lamparray"}

func optionsPlusRunning() bool { return len(findProcesses(optionsPlusNames[:2]...)) > 0 }

func quitOptionsPlus() int {
	n := 0
	for _, pid := range findProcesses(optionsPlusNames[:2]...) {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(h, 0) == nil {
			n++
		}
		windows.CloseHandle(h)
	}
	logf("closed %d Logi Options+ processes", n)
	return n
}

// ---------- autostart ----------

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Mishk")
	return err == nil && strings.Contains(strings.ToLower(v), strings.ToLower(exePath()))
}

func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue("Mishk", `"`+exePath()+`" --tray`)
	}
	err = k.DeleteValue("Mishk")
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
