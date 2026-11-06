package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const logitechVID = 0x046D

type hidInfo struct {
	Path      string
	VID, PID  uint16
	UsagePage uint16
	Usage     uint16
	InLen     int
	OutLen    int
	Product   string
}

type hidAttributes struct {
	Size      uint32
	VendorID  uint16
	ProductID uint16
	Version   uint16
	_         uint16
}

func hidGUID() windows.GUID {
	var g windows.GUID
	pHidDGetHidGuid.Call(uintptr(unsafe.Pointer(&g)))
	return g
}

func openHID(path string, access uint32, overlapped bool) (windows.Handle, error) {
	flags := uint32(0)
	if overlapped {
		flags = windows.FILE_FLAG_OVERLAPPED
	}
	return windows.CreateFile(u16(path), access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, flags, 0)
}

func queryHID(path string) (*hidInfo, error) {
	h, err := openHID(path, 0, false)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	attr := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
	if r, _, _ := pHidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&attr))); r == 0 {
		return nil, errors.New("HidD_GetAttributes failed")
	}
	info := &hidInfo{Path: path, VID: attr.VendorID, PID: attr.ProductID}
	if info.VID != logitechVID {
		return info, nil
	}
	var pp uintptr
	if r, _, _ := pHidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&pp))); r != 0 {
		var caps [64]uint16
		pHidPGetCaps.Call(pp, uintptr(unsafe.Pointer(&caps[0])))
		pHidDFreePreparsedData.Call(pp)
		info.Usage, info.UsagePage = caps[0], caps[1]
		info.InLen, info.OutLen = int(caps[2]), int(caps[3])
	}
	var buf [128]uint16
	if r, _, _ := pHidDGetProductString.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2)); r != 0 {
		info.Product = windows.UTF16ToString(buf[:])
	}
	return info, nil
}

var colRe = regexp.MustCompile(`&col[0-9a-f]+`)

// groupKey makes the short and long collections of one receiver share a key.
func groupKey(path string) string {
	p := strings.ToLower(path)
	parts := strings.Split(p, "#")
	if len(parts) < 3 {
		return p
	}
	k1 := colRe.ReplaceAllString(parts[1], "")
	k2 := parts[2]
	if i := strings.LastIndex(k2, "&"); i > 0 {
		k2 = k2[:i]
	}
	return k1 + "#" + k2
}

type hidGroup struct {
	key         string
	pid         uint16
	product     string
	short, long *hidInfo
}

func isReceiverPID(pid uint16) bool { return pid&0xFF00 == 0xC500 }

// enumerateHIDPP finds Logitech HID++ interfaces, grouped per physical receiver/device.
func enumerateHIDPP() []*hidGroup {
	g := hidGUID()
	paths, err := windows.CM_Get_Device_Interface_List("", &g, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		logf("hid: list interfaces: %v", err)
		return nil
	}
	groups := map[string]*hidGroup{}
	for _, p := range paths {
		lp := strings.ToLower(p)
		if !strings.Contains(lp, "046d") {
			continue
		}
		info, err := queryHID(p)
		if err != nil || info.VID != logitechVID {
			continue
		}
		hidpp := (info.UsagePage == 0xFF00 && (info.Usage == 0x0001 || info.Usage == 0x0002)) ||
			(info.UsagePage == 0xFF43 && (info.Usage == 0x0202 || info.Usage == 0x0201))
		if !hidpp {
			continue
		}
		k := groupKey(p)
		gr := groups[k]
		if gr == nil {
			gr = &hidGroup{key: k, pid: info.PID, product: info.Product}
			groups[k] = gr
		}
		if info.OutLen == 7 {
			gr.short = info
		} else if info.OutLen >= 20 {
			if gr.long == nil || info.OutLen == 20 {
				gr.long = info
			}
		}
	}
	var out []*hidGroup
	for _, gr := range groups {
		if gr.long != nil {
			out = append(out, gr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// ---------- handles ----------

type hidHandle struct {
	h      windows.Handle
	inLen  int
	outLen int
	wmu    sync.Mutex
	wev    windows.Handle
}

func openHandle(info *hidInfo) (*hidHandle, error) {
	h, err := openHID(info.Path, windows.GENERIC_READ|windows.GENERIC_WRITE, true)
	if err != nil {
		return nil, err
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &hidHandle{h: h, inLen: info.InLen, outLen: info.OutLen, wev: ev}, nil
}

func (hh *hidHandle) write(rep []byte) error {
	hh.wmu.Lock()
	defer hh.wmu.Unlock()
	buf := make([]byte, hh.outLen)
	copy(buf, rep)
	ov := windows.Overlapped{HEvent: hh.wev}
	windows.ResetEvent(hh.wev)
	var n uint32
	err := windows.WriteFile(hh.h, buf, &n, &ov)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return err
	}
	ev, _ := windows.WaitForSingleObject(hh.wev, 1000)
	if ev != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(hh.h, &ov)
		windows.GetOverlappedResult(hh.h, &ov, &n, true)
		return errors.New("write timeout")
	}
	return windows.GetOverlappedResult(hh.h, &ov, &n, false)
}

// readLoop delivers reports until the handle is closed or the device disappears.
func (hh *hidHandle) readLoop(deliver func([]byte), dead func(error)) {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		dead(err)
		return
	}
	defer windows.CloseHandle(ev)
	n := hh.inLen
	if n < 20 {
		n = 64
	}
	buf := make([]byte, n)
	for {
		ov := windows.Overlapped{HEvent: ev}
		windows.ResetEvent(ev)
		var got uint32
		err := windows.ReadFile(hh.h, buf, &got, &ov)
		if err == windows.ERROR_IO_PENDING {
			err = windows.GetOverlappedResult(hh.h, &ov, &got, true)
		}
		if err != nil {
			dead(err)
			return
		}
		if got > 0 {
			deliver(append([]byte(nil), buf[:got]...))
		}
	}
}

func (hh *hidHandle) close() {
	windows.CancelIoEx(hh.h, nil)
	windows.CloseHandle(hh.h)
	windows.CloseHandle(hh.wev)
}

type winLink struct {
	short, long *hidHandle
	closed      atomic.Bool
}

func (l *winLink) HasShort() bool { return l.short != nil }

func (l *winLink) Send(rep []byte) error {
	if l.closed.Load() {
		return ErrClosed
	}
	if rep[0] == reportShort {
		if l.short == nil {
			return errors.New("no short collection")
		}
		return l.short.write(rep)
	}
	return l.long.write(rep)
}

func (l *winLink) Close() {
	if l.closed.Swap(true) {
		return
	}
	if l.short != nil {
		l.short.close()
	}
	l.long.close()
}

// ---------- scanner ----------

type Scanner struct {
	e       *Engine
	mu      sync.Mutex
	open    map[string]*winLink
	retryAt map[string]time.Time
	trigger chan struct{}
}

func NewScanner(e *Engine) *Scanner {
	return &Scanner{e: e, open: map[string]*winLink{}, retryAt: map[string]time.Time{}, trigger: make(chan struct{}, 1)}
}

func (s *Scanner) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *Scanner) Run() {
	s.scan()
	t := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-t.C:
		case <-s.trigger:
			time.Sleep(700 * time.Millisecond) // let Windows finish creating all collections
		}
		s.scan()
	}
}

func (s *Scanner) scan() {
	groups := enumerateHIDPP()
	present := map[string]bool{}
	for _, g := range groups {
		present[g.key] = true
		s.mu.Lock()
		_, have := s.open[g.key]
		wait := time.Now().Before(s.retryAt[g.key])
		s.mu.Unlock()
		if have || wait {
			continue
		}
		if err := s.attach(g); err != nil {
			logf("hid: open %s: %v", g.key, err)
		}
	}
	s.mu.Lock()
	var gone []string
	for k := range s.open {
		if !present[k] {
			gone = append(gone, k)
		}
	}
	s.mu.Unlock()
	for _, k := range gone {
		s.detach(k)
	}
}

func (s *Scanner) attach(g *hidGroup) error {
	long, err := openHandle(g.long)
	if err != nil {
		return err
	}
	link := &winLink{long: long}
	if g.short != nil {
		if sh, err := openHandle(g.short); err == nil {
			link.short = sh
		}
	}
	kind := "direct"
	product := g.product
	if isReceiverPID(g.pid) {
		kind = "receiver"
		switch {
		case g.pid == 0xC548:
			product = "Logi Bolt"
		case product == "":
			product = fmt.Sprintf("Unifying receiver %04X", g.pid)
		}
		if !strings.Contains(strings.ToLower(product), "bolt") && g.pid != 0xC548 {
			product = "Unifying " + product
		}
	}
	s.mu.Lock()
	s.open[g.key] = link
	s.mu.Unlock()
	logf("hid: opened %s pid=%04X kind=%s product=%q short=%v", g.key, g.pid, kind, product, link.short != nil)
	ch := s.e.AddLink(g.key, kind, product, link)
	dead := func(err error) {
		if link.closed.Load() {
			return
		}
		logf("hid: %s stopped: %v", g.key, err)
		go s.detach(g.key)
	}
	go long.readLoop(ch.Deliver, dead)
	if link.short != nil {
		go link.short.readLoop(ch.Deliver, dead)
	}
	return nil
}

func (s *Scanner) detach(key string) {
	s.mu.Lock()
	link := s.open[key]
	delete(s.open, key)
	s.retryAt[key] = time.Now().Add(3 * time.Second)
	s.mu.Unlock()
	if link == nil {
		return
	}
	s.e.RemoveLink(key) // closes the channel and the link
	s.Trigger()
}

func (s *Scanner) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, l := range s.open {
		l.Close()
		delete(s.open, k)
	}
}
