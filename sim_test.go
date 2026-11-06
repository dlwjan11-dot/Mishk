package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- a simulated Bolt receiver with an MX Master 3S in slot 2 ----

type simMouse struct {
	mu       sync.Mutex
	chMu     sync.Mutex
	ch       *Channel
	idx      byte
	online   bool
	features []uint16 // index = position
	dpi      int
	ssMode   byte
	ssAuto   byte
	ssTorque byte
	hires    byte
	thumbDiv byte
	thumbInv byte
	report   map[uint16]byte
	notifFlags []byte
	pairReq  []byte
	sent     []string
	silent   bool
}

func newSimMouse() *simMouse {
	return &simMouse{idx: 2, online: true,
		features: []uint16{0x0000, 0x0001, 0x0003, 0x0005, 0x1004, 0x1814, 0x1B04, 0x2201, 0x2111, 0x2121, 0x2150},
		dpi:      1000, ssMode: 2, ssAuto: 10, ssTorque: 50, hires: 0x02, report: map[uint16]byte{}}
}

var simCIDs = []struct {
	cid   uint16
	flags byte
	f2    byte
}{
	{0x0050, 0x01, 0}, {0x0051, 0x01, 0}, {0x0052, 0x31, 0x01}, {0x0053, 0x31, 0x01},
	{0x0056, 0x31, 0x01}, {0x00C3, 0x31, 0x03}, {0x00C4, 0x31, 0x03}, {0x00D7, 0xB4, 0x03},
}

func (s *simMouse) HasShort() bool { return true }
func (s *simMouse) Close()         {}

func (s *simMouse) Send(rep []byte) error {
	s.mu.Lock()
	s.sent = append(s.sent, hexs(rep))
	s.mu.Unlock()
	go s.answer(append([]byte(nil), rep...))
	return nil
}

func (s *simMouse) channel() *Channel {
	for {
		s.chMu.Lock()
		c := s.ch
		s.chMu.Unlock()
		if c != nil {
			return c
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *simMouse) deliver(b ...byte) {
	time.Sleep(2 * time.Millisecond)
	s.channel().Deliver(b)
}

func long(b ...byte) []byte {
	out := make([]byte, 20)
	copy(out, b)
	return out
}

func (s *simMouse) answer(r []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := r[1]
	if r[0] == reportShort { // receiver registers
		switch {
		case r[2] == 0x80 && r[3] == 0x00:
			s.notifFlags = r[4:7]
			go s.deliver(reportShort, 0xFF, 0x80, 0x00, 0, 0, 0)
		case r[2] == 0x80 && r[3] == 0xC0 && r[5] == 0x01: // start discovery
			go s.deliver(reportShort, 0xFF, 0x80, 0xC0, 0, 0, 0)
			go func() {
				time.Sleep(20 * time.Millisecond)
				s.channel().Deliver([]byte{reportShort, 0xFF, 0x53, 0x00, 0, 0, 0})
				time.Sleep(20 * time.Millisecond)
				s.channel().Deliver(long(reportLong, 0xFF, 0x4F, 0x07, 0x00, 0x00, 0, 0x02, 0, 0, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0, 0, 0x00))
				s.channel().Deliver(long(append([]byte{reportLong, 0xFF, 0x4F, 0x07, 0x00, 0x01, 12}, []byte("MX Master 3S")...)...))
			}()
		case r[2] == 0x80 && r[3] == 0xC0:
			go s.deliver(reportShort, 0xFF, 0x80, 0xC0, 0, 0, 0)
		case r[2] == 0x80 && r[3] == 0x02:
			go s.deliver(reportShort, 0xFF, 0x80, 0x02, 0, 0, 0)
			if s.online {
				go s.deliver(reportShort, s.idx, 0x41, 0x10, 0x02, 0x34, 0xB0)
			}
		}
		return
	}
	if r[0] == reportLong && r[2] == 0x82 && r[3] == 0xC1 { // Bolt pair / unpair
		s.pairReq = append([]byte(nil), r[4:16]...)
		go s.deliver(long(reportLong, 0xFF, 0x82, 0xC1)...)
		if r[4] == 0x01 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				ch := s.channel()
				ch.Deliver([]byte{reportShort, 0xFF, 0x54, 0x00, 0, 0, 0})
				ch.Deliver(long(append([]byte{reportLong, 0xFF, 0x4D, 0x00}, []byte("000613")...)...))
				ch.Deliver([]byte{reportShort, 0xFF, 0x4E, 0x00, 0, 0, 0})
				ch.Deliver([]byte{reportShort, 0xFF, 0x4E, 0x01, 0, 0, 0})
				ch.Deliver([]byte{reportShort, 0xFF, 0x4E, 0x01, 0, 0, 0})
				time.Sleep(30 * time.Millisecond)
				s.mu.Lock()
				s.online = true
				s.mu.Unlock()
				ch.Deliver(long(reportLong, 0xFF, 0x54, 0x02, 0, 0, 0, 0, 0, 0, 0, s.idx))
			}()
		}
		return
	}
	if dev != s.idx || !s.online || s.silent {
		if dev != s.idx {
			go s.deliver(reportShort, dev, 0x8F, r[2], r[3], 0x08, 0)
		}
		return
	}
	fi, fnsw := r[2], r[3]
	fn := fnsw >> 4
	p := r[4:]
	if int(fi) >= len(s.features) {
		go s.deliver(long(reportLong, dev, 0xFF, fi, fnsw, 0x06)...)
		return
	}
	reply := func(params ...byte) {
		go s.deliver(long(append([]byte{reportLong, dev, fi, fnsw}, params...)...)...)
	}
	switch s.features[fi] {
	case 0x0000:
		if fn == 0 {
			want := uint16(p[0])<<8 | uint16(p[1])
			for i, f := range s.features {
				if f == want {
					reply(byte(i), 0, 0)
					return
				}
			}
			reply(0)
		} else if fn == 1 {
			reply(4, 5, p[2])
		}
	case 0x0005:
		name := "MX Master 3S"
		if fn == 0 {
			reply(byte(len(name)))
		} else {
			off := int(p[0])
			end := off + 16
			if end > len(name) {
				end = len(name)
			}
			reply([]byte(name[off:end])...)
		}
	case 0x1004:
		reply(25, 0x04, 0, 0)
	case 0x1814:
		reply(3, 0)
	case 0x1B04:
		switch fn {
		case 0:
			reply(byte(len(simCIDs)))
		case 1:
			c := simCIDs[p[0]]
			reply(byte(c.cid>>8), byte(c.cid), 0, 0, c.flags, 0, 0, 0, c.f2)
		case 2:
			cid := uint16(p[0])<<8 | uint16(p[1])
			reply(p[0], p[1], s.report[cid])
		case 3:
			cid := uint16(p[0])<<8 | uint16(p[1])
			bf := p[2]
			cur := s.report[cid]
			if bf&0x02 != 0 {
				cur = cur&^0x01 | bf&0x01
			}
			if bf&0x20 != 0 {
				cur = cur&^0x10 | bf&0x10
			}
			s.report[cid] = cur
			reply(p[0], p[1], cur)
		}
	case 0x2201:
		switch fn {
		case 1:
			reply(0, 0x00, 0xC8, 0xE0, 0x32, 0x1F, 0x40, 0, 0)
		case 2:
			reply(0, byte(s.dpi>>8), byte(s.dpi), 0x03, 0xE8)
		case 3:
			s.dpi = int(p[1])<<8 | int(p[2])
			reply(p[0], p[1], p[2])
		}
	case 0x2111:
		switch fn {
		case 1:
			reply(s.ssMode, s.ssAuto, s.ssTorque)
		case 2:
			if p[0] != 0 {
				s.ssMode = p[0]
			}
			if p[1] != 0 {
				s.ssAuto = p[1]
			}
			if p[2] != 0 {
				s.ssTorque = p[2]
			}
			reply()
		}
	case 0x2121:
		if fn == 1 {
			reply(s.hires)
		} else if fn == 2 {
			s.hires = p[0]
			reply(p[0])
		}
	case 0x2150:
		switch fn {
		case 0:
			reply(0, 18, 0, 120, 0, 0)
		case 1:
			reply(s.thumbDiv, s.thumbInv)
		case 2:
			s.thumbDiv, s.thumbInv = p[0], p[1]
			reply(p[0], p[1])
		}
	default:
		go s.deliver(long(reportLong, dev, 0xFF, fi, fnsw, 0x09)...)
	}
}

func (s *simMouse) featIndex(f uint16) byte {
	for i, x := range s.features {
		if x == f {
			return byte(i)
		}
	}
	return 0
}

func (s *simMouse) buttons(cids ...uint16) {
	b := []byte{reportLong, s.idx, s.featIndex(0x1B04), 0x00}
	for _, c := range cids {
		b = append(b, byte(c>>8), byte(c))
	}
	s.ch.Deliver(long(b...))
}

func (s *simMouse) rawXY(dx, dy int16) {
	s.ch.Deliver(long(reportLong, s.idx, s.featIndex(0x1B04), 0x10, byte(uint16(dx)>>8), byte(dx), byte(uint16(dy)>>8), byte(dy)))
}

func (s *simMouse) thumb(rot int16) {
	s.ch.Deliver(long(reportLong, s.idx, s.featIndex(0x2150), 0x00, byte(uint16(rot)>>8), byte(rot), 0, 0, 2, 0))
}

// ---- recorder ----

type recorder struct {
	mu  sync.Mutex
	evs []string
}

var vkRev = map[uint16]string{}

func init() {
	for k, v := range vkNames {
		if _, ok := vkRev[v]; !ok || len(k) < len(vkRev[v]) {
			vkRev[v] = k
		}
	}
}

func keyName(vk uint16) string {
	if vk >= 0x41 && vk <= 0x5A {
		return string(rune('a' + vk - 0x41))
	}
	if n, ok := vkRev[vk]; ok {
		return n
	}
	return fmt.Sprintf("vk%02X", vk)
}

func (r *recorder) add(s string) { r.mu.Lock(); r.evs = append(r.evs, s); r.mu.Unlock() }
func (r *recorder) Key(vk uint16, down bool) {
	r.add(fmt.Sprintf("%s%s", map[bool]string{true: "+", false: "-"}[down], keyName(vk)))
}
func (r *recorder) MouseButton(b string, down bool) {
	r.add(fmt.Sprintf("%smouse:%s", map[bool]string{true: "+", false: "-"}[down], b))
}
func (r *recorder) Wheel(d int, h bool) { r.add(fmt.Sprintf("wheel:%d:%v", d, h)) }
func (r *recorder) Run(t string)        { r.add("run:" + t) }
func (r *recorder) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := strings.Join(r.evs, " ")
	r.evs = nil
	return s
}

// ---- tests ----

func settle(e *Engine) {
	for i := 0; i < 3; i++ {
		done := make(chan struct{})
		e.post(func() { close(done) })
		<-done
		time.Sleep(30 * time.Millisecond)
	}
}

func waitReady(t *testing.T, e *Engine) DeviceView {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range e.Devices() {
			if d.Ready {
				settle(e)
				return d
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("device never became ready\n" + logText())
	return DeviceView{}
}

func setup(t *testing.T) (*Engine, *simMouse, *recorder, *string) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	rec := &recorder{}
	fg := new(string)
	e := NewEngine(DefaultConfig(), store, rec, func() string { return *fg })
	e.Start()
	sim := newSimMouse()
	c := e.AddLink("bolt1", "receiver", "USB Receiver (Bolt)", sim)
	sim.chMu.Lock()
	sim.ch = c
	sim.chMu.Unlock()
	t.Cleanup(func() { e.Stop() })
	return e, sim, rec, fg
}

func TestDiscoveryAndDefaults(t *testing.T) {
	e, sim, _, _ := setup(t)
	d := waitReady(t, e)
	if d.Name != "MX Master 3S" || d.Conn != "bolt" || d.Battery != 25 || d.DPI != 1000 {
		t.Fatalf("bad view: %+v", d)
	}
	if d.DPIMin != 200 || d.DPIMax != 8000 || d.DPIStep != 50 {
		t.Fatalf("dpi range %d-%d/%d", d.DPIMin, d.DPIMax, d.DPIStep)
	}
	if !d.HasTorque || d.SSMode != "smart" || d.Hosts != 3 {
		t.Fatalf("ss/hosts: %+v", d)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if sim.report[0x00C3] != 0x11 {
		t.Fatalf("gesture button should be diverted with raw XY, got %02X", sim.report[0x00C3])
	}
	if sim.report[0x0053] != 0 {
		t.Fatalf("back should stay native")
	}
	if fmt.Sprintf("%X", sim.notifFlags) != "000900" {
		t.Fatalf("notification flags %X", sim.notifFlags)
	}
}

func TestGestures(t *testing.T) {
	e, sim, rec, _ := setup(t)
	waitReady(t, e)
	rec.take()
	sim.buttons(0x00C3)
	sim.rawXY(-40, 5)
	sim.rawXY(-50, 3)
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "+ctrl +win +left -left -win -ctrl" {
		t.Fatalf("swipe left: %q", got)
	}
	sim.buttons(0x00C3)
	sim.rawXY(3, 2)
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "+win +tab -tab -win" {
		t.Fatalf("click: %q", got)
	}
	sim.buttons(0x00C3)
	sim.rawXY(0, 90)
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "+win +d -d -win" {
		t.Fatalf("down: %q", got)
	}
}

func TestRemapHoldAndProfiles(t *testing.T) {
	e, sim, rec, fg := setup(t)
	waitReady(t, e)
	c := e.Config()
	c.Buttons["back"] = Action{Type: "keys", Value: "Ctrl+Z"}
	c.Profiles = []Profile{{ID: "p1", Name: "Photoshop", Exe: "Photoshop.exe",
		Buttons: map[string]Action{"back": {Type: "keys", Value: "Space"}, "forward": {Type: "builtin", Value: "precision"}},
		Thumb:   WheelAction{Type: "brush"}}}
	if err := e.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	settle(e)
	sim.mu.Lock()
	back, fwd, thumb := sim.report[0x0053], sim.report[0x0056], sim.thumbDiv
	sim.mu.Unlock()
	if back != 0x01 || fwd != 0x01 || thumb != 1 {
		t.Fatalf("diversions back=%02X fwd=%02X thumb=%d", back, fwd, thumb)
	}
	rec.take()
	// Global app
	*fg = "chrome.exe"
	sim.buttons(0x0053)
	settle(e)
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "+ctrl +z -z -ctrl" {
		t.Fatalf("global back: %q", got)
	}
	// Forward is diverted only for Photoshop; in Chrome it must act as native forward.
	sim.buttons(0x0056)
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "+mouse:forward -mouse:forward" {
		t.Fatalf("native forward emulation: %q", got)
	}
	// Thumb wheel default in chrome: smooth horizontal scroll
	sim.thumb(20)
	settle(e)
	if got := rec.take(); got != "wheel:360:true" {
		t.Fatalf("hscroll: %q", got)
	}
	// Photoshop
	*fg = "photoshop.exe"
	e.fgCacheAt = time.Time{}
	sim.buttons(0x0053)
	settle(e)
	if got := rec.take(); got != "+space" {
		t.Fatalf("hold space down: %q", got)
	}
	sim.buttons()
	settle(e)
	if got := rec.take(); got != "-space" {
		t.Fatalf("space up: %q", got)
	}
	// precision mode on forward
	sim.buttons(0x0056)
	settle(e)
	sim.mu.Lock()
	dpiHeld := sim.dpi
	sim.mu.Unlock()
	sim.buttons()
	settle(e)
	sim.mu.Lock()
	dpiAfter := sim.dpi
	sim.mu.Unlock()
	if dpiHeld != 400 || dpiAfter != 1000 {
		t.Fatalf("precision: held=%d after=%d", dpiHeld, dpiAfter)
	}
	// Thumb: brush size; diverted res 120 / (6+5*2)=7 counts per step
	sim.thumb(15)
	settle(e)
	if got := rec.take(); got != "+] -] +] -]" {
		t.Fatalf("brush: %q", got)
	}
}

func TestSettingsApplied(t *testing.T) {
	e, sim, _, _ := setup(t)
	waitReady(t, e)
	c := e.Config()
	c.DPI = 1625 // gets snapped to the 50 step
	c.SmartShift = &SmartShift{Mode: "ratchet", Torque: 80}
	tr := true
	c.InvertScroll = &tr
	c.InvertThumb = &tr
	_ = e.SetConfig(c)
	settle(e)
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if sim.dpi != 1650 && sim.dpi != 1600 {
		t.Fatalf("dpi %d", sim.dpi)
	}
	if sim.ssMode != 2 || sim.ssAuto != 0xFF || sim.ssTorque != 80 {
		t.Fatalf("smartshift %d %d %d", sim.ssMode, sim.ssAuto, sim.ssTorque)
	}
	if sim.hires != 0x06 || sim.thumbInv != 1 {
		t.Fatalf("wheel %02X thumbInv %d", sim.hires, sim.thumbInv)
	}
}

func TestReapplyAfterSleepAndDisconnect(t *testing.T) {
	e, sim, _, _ := setup(t)
	waitReady(t, e)
	// mouse forgets diversion (e.g. woke up from deep sleep)
	sim.mu.Lock()
	sim.report = map[uint16]byte{}
	sim.mu.Unlock()
	e.post(func() { e.watchdog(false) })
	settle(e)
	settle(e)
	sim.mu.Lock()
	r := sim.report[0x00C3]
	sim.mu.Unlock()
	if r != 0x11 {
		t.Fatalf("diversion not re-applied: %02X\n%s", r, logText())
	}
	// receiver says the link is gone
	sim.ch.Deliver([]byte{reportShort, 2, 0x41, 0x10, 0x42, 0x34, 0xB0})
	settle(e)
	if d := e.Devices()[0]; d.Online {
		t.Fatal("should be offline")
	}
	// and back
	sim.ch.Deliver([]byte{reportShort, 2, 0x41, 0x10, 0x02, 0x34, 0xB0})
	time.Sleep(700 * time.Millisecond)
	waitReady(t, e)
}

func TestPausedUndiverts(t *testing.T) {
	e, sim, _, _ := setup(t)
	waitReady(t, e)
	c := e.Config()
	c.Paused = true
	_ = e.SetConfig(c)
	settle(e)
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if sim.report[0x00C3] != 0 {
		t.Fatalf("paused but still diverted: %02X", sim.report[0x00C3])
	}
}

func TestParseKeys(t *testing.T) {
	cases := map[string]string{
		"Ctrl+Shift+Z": "11 10 5A", "Win+Shift+S": "5B 10 53", "Ctrl++": "11 BB", "F12": "7B", "Alt+F4": "12 73", "[": "DB",
	}
	for in, want := range cases {
		k, err := ParseKeys(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var parts []string
		for _, v := range k {
			parts = append(parts, fmt.Sprintf("%02X", v))
		}
		if got := strings.Join(parts, " "); got != want {
			t.Fatalf("%s: got %s want %s", in, got, want)
		}
	}
	if _, err := ParseKeys("Ctrl+Blah"); err == nil {
		t.Fatal("expected error")
	}
}


func TestBoltPairing(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	e := NewEngine(DefaultConfig(), NewConfigStore(dir), rec, func() string { return "" })
	e.Start()
	defer e.Stop()
	sim := newSimMouse()
	sim.online = false // not paired yet
	c := e.AddLink("bolt1", "receiver", "Logi Bolt", sim)
	sim.chMu.Lock()
	sim.ch = c
	sim.chMu.Unlock()
	time.Sleep(2500 * time.Millisecond) // let the slot probing finish
	settle(e)
	if n := len(e.Devices()); n != 0 {
		t.Fatalf("expected no devices before pairing, got %d", n)
	}
	e.StartPairing("bolt1")
	var passkey *PairingView
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := e.Pairing(); p != nil && p.Stage == "passkey" && p.Pressed == 2 {
			passkey = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if passkey == nil {
		t.Fatalf("never reached passkey stage: %+v\n%s", e.Pairing(), logText())
	}
	if passkey.Name != "MX Master 3S" || passkey.Kind != "mouse" || passkey.Keyboard {
		t.Fatalf("pairing view: %+v", passkey)
	}
	// 613 = 1001100101
	if got := strings.Join(passkey.Clicks, ","); got != "right,left,left,right,right,left,left,right,left,right" {
		t.Fatalf("clicks %s", got)
	}
	sim.mu.Lock()
	req := hexs(sim.pairReq)
	sim.mu.Unlock()
	if req != "01 00 AA BB CC DD EE FF 00 0A 00 00" {
		t.Fatalf("pair request %s", req)
	}
	d := waitReady(t, e)
	if d.Name != "MX Master 3S" || d.Slot != 2 {
		t.Fatalf("paired device %+v", d)
	}
	if p := e.Pairing(); p.Stage != "done" || p.Active {
		t.Fatalf("final pairing state %+v", p)
	}
	if r := e.Receivers(); len(r) != 1 || r[0].Kind != "bolt" || r[0].Devices != 1 {
		t.Fatalf("receivers %+v", r)
	}
	e.Unpair(d.Key)
	settle(e)
	sim.mu.Lock()
	req = hexs(sim.pairReq[:2])
	sim.mu.Unlock()
	if req != "03 02" || len(e.Devices()) != 0 {
		t.Fatalf("unpair req %s devices %d", req, len(e.Devices()))
	}
}
