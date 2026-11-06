package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type cidInfo struct {
	CID        uint16
	Divertable bool
	RawXY      bool
}

type Device struct {
	key    string
	ch     *Channel
	link   *linkState
	idx    byte
	Conn   string // bolt | unifying | bluetooth | receiver
	Name   string
	WPID   uint16
	Online bool
	Ready  bool

	feat map[uint16]byte
	cids map[uint16]cidInfo

	Battery  int // -1 unknown
	Charging bool
	lowAlert bool

	DPI, DPIMin, DPIMax, DPIStep int
	savedDPI                     int // DPI before precision mode
	tempDPI                      bool

	ssEnh                      bool
	ssMode, ssAuto, ssTorque   byte
	hiresFlags                 byte
	thumbNative, thumbDivRes   int
	thumbInvert, thumbDiverted bool
	Hosts, CurHost             int

	diverted map[uint16]byte // cid -> bfield currently applied
	pressed  map[uint16]bool
	held     map[uint16]func()
	gesture  *gestureState
	thumbAcc int
	thumbRem int

	failures int
	lastInit time.Time
	initGen  int
}

type gestureState struct {
	cid    uint16
	g      *Gestures
	dx, dy int
}

type linkState struct {
	id       string
	kind     string // receiver | direct
	product  string
	ch       *Channel
	seen     map[byte]bool
	attached time.Time
}

type Engine struct {
	mu      sync.Mutex
	cfg     *Config
	store   *ConfigStore
	act     Actuator
	fgFunc  func() string
	links   map[string]*linkState
	devices map[string]*Device
	cmds    chan func()
	stop    chan struct{}

	fgMu      sync.Mutex
	fgCache   string
	fgCacheAt time.Time

	generic *Device  // any mouse, through Windows (see generic.go)
	sys     SysMouse // Windows mouse settings

	OnChange func()
	OnNotify func(title, body string)

	pair *pairingState
}

func NewEngine(cfg *Config, store *ConfigStore, act Actuator, fg func() string) *Engine {
	return &Engine{
		cfg: cfg, store: store, act: act, fgFunc: fg,
		links: map[string]*linkState{}, devices: map[string]*Device{},
		cmds: make(chan func(), 4096), stop: make(chan struct{}),
	}
}

// ---------- worker ----------

func (e *Engine) Start() {
	go e.worker()
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-e.stop:
				return
			case <-t.C:
				n++
				full := n%75 == 0 // ~5 minutes: refresh battery etc.
				e.post(func() { e.watchdog(full) })
			}
		}
	}()
}

func (e *Engine) Stop() {
	done := make(chan struct{})
	e.post(func() {
		for _, d := range e.devices {
			e.releaseAll(d)
			if d.Ready && d.Online {
				e.undivertAll(d)
			}
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	close(e.stop)
}

func (e *Engine) post(f func()) {
	select {
	case e.cmds <- f:
	default:
		logf("engine: command queue full, dropping event")
	}
}

func (e *Engine) worker() {
	for {
		select {
		case <-e.stop:
			return
		case f := <-e.cmds:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logf("engine: recovered from panic: %v", r)
					}
				}()
				f()
			}()
		}
	}
}

func (e *Engine) changed() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

// postAfter runs f on the worker after a delay.
func (e *Engine) postAfter(d time.Duration, f func()) {
	time.AfterFunc(d, func() { e.post(f) })
}

// ---------- links ----------

// AddLink registers an opened HID transport. kind = "receiver" or "direct".
func (e *Engine) AddLink(id, kind, product string, link Link) *Channel {
	ls := &linkState{id: id, kind: kind, product: product, seen: map[byte]bool{}, attached: time.Now()}
	ch := NewChannel(link, kind == "receiver", func(pkt []byte) {
		e.post(func() { e.handlePacket(ls, pkt) })
	})
	ls.ch = ch
	e.mu.Lock()
	e.links[id] = ls
	e.mu.Unlock()
	logf("link added: %s (%s, %s)", id, kind, product)
	e.post(func() { e.initLink(ls) })
	return ch
}

func (e *Engine) RemoveLink(id string) {
	e.mu.Lock()
	ls := e.links[id]
	delete(e.links, id)
	e.mu.Unlock()
	if ls == nil {
		return
	}
	logf("link removed: %s", id)
	e.post(func() {
		e.mu.Lock()
		var gone []*Device
		for k, d := range e.devices {
			if d.link == ls {
				gone = append(gone, d)
				delete(e.devices, k)
			}
		}
		e.mu.Unlock()
		for _, d := range gone {
			e.releaseAll(d)
		}
		ls.ch.Close()
		e.changed()
	})
}

func (e *Engine) HasLink(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.links[id]
	return ok
}

func (e *Engine) initLink(ls *linkState) {
	if ls.kind == "direct" {
		d := e.device(ls, devReceiver)
		d.Conn = "bluetooth"
		e.initDevice(d)
		return
	}
	// Ask the receiver to tell us about connections, then to list paired devices.
	if _, err := ls.ch.Register(devReceiver, 0x80, 0x00, 0x00, 0x09, 0x00); err != nil {
		logf("receiver %s: enable notifications: %v", ls.id, err)
	}
	if _, err := ls.ch.Register(devReceiver, 0x80, 0x02, 0x02, 0x00, 0x00); err != nil {
		logf("receiver %s: list devices: %v", ls.id, err)
	}
	// Fallback in case the connection notifications never arrive.
	e.postAfter(1500*time.Millisecond, func() {
		e.mu.Lock()
		_, alive := e.links[ls.id]
		e.mu.Unlock()
		if !alive {
			return
		}
		for i := byte(1); i <= 6; i++ {
			if ls.seen[i] {
				continue
			}
			ls.ch.Timeout = 600 * time.Millisecond
			_, _, err := ls.ch.Ping(i)
			ls.ch.Timeout = 1200 * time.Millisecond
			if err == nil {
				ls.seen[i] = true
				d := e.device(ls, i)
				e.initDevice(d)
			}
		}
	})
}

func (e *Engine) device(ls *linkState, idx byte) *Device {
	key := fmt.Sprintf("%s#%d", ls.id, idx)
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.devices[key]
	if d == nil {
		conn := "receiver"
		if strings.Contains(strings.ToLower(ls.product), "bolt") || strings.EqualFold(ls.product, "c548") {
			conn = "bolt"
		} else if strings.Contains(strings.ToLower(ls.product), "unifying") {
			conn = "unifying"
		}
		d = &Device{key: key, ch: ls.ch, link: ls, idx: idx, Conn: conn, Battery: -1,
			feat: map[uint16]byte{}, cids: map[uint16]cidInfo{}, diverted: map[uint16]byte{},
			pressed: map[uint16]bool{}, held: map[uint16]func(){}}
		e.devices[key] = d
	}
	return d
}

// ---------- device setup ----------

func (e *Engine) initDevice(d *Device) {
	d.initGen++
	d.lastInit = time.Now()
	maj, min, err := d.ch.Ping(d.idx)
	if err != nil {
		if d.Online || d.Name == "" && d.initGen <= 2 {
			logf("[%s] ping: %v", d.key, err)
		}
		e.setOnline(d, false)
		return
	}
	logf("[%s] HID++ %d.%d", d.key, maj, min)
	if maj < 2 {
		e.setOnline(d, false)
		return
	}
	feats := map[uint16]byte{}
	for _, f := range []uint16{featDeviceName, featBattery, featUnifiedBatt, featChangeHost, featReprogV4,
		featAdjustableDPI, featSmartShift, featSmartShiftEnh, featHiResWheel, featThumbWheel} {
		idx, err := d.ch.FeatureIndex(d.idx, f)
		if err != nil {
			if errors.Is(err, ErrTimeout) {
				logf("[%s] went quiet during setup", d.key)
				e.setOnline(d, false)
				return
			}
			continue
		}
		if idx != 0 {
			feats[f] = idx
		}
	}
	name := d.Name
	if fi, ok := feats[featDeviceName]; ok {
		if n := readName(d.ch, d.idx, fi); n != "" {
			name = n
		}
	}
	cids := map[uint16]cidInfo{}
	if fi, ok := feats[featReprogV4]; ok {
		if p, err := d.ch.Call(d.idx, fi, 0); err == nil {
			count := int(p[0])
			if count > 40 {
				count = 40
			}
			for i := 0; i < count; i++ {
				q, err := d.ch.Call(d.idx, fi, 1, byte(i))
				if err != nil {
					continue
				}
				cid := be16(q[0:2])
				flags := uint16(q[4]) | uint16(q[8])<<8
				cids[cid] = cidInfo{CID: cid, Divertable: flags&0x20 != 0, RawXY: flags&0x0100 != 0}
			}
		}
	}
	e.mu.Lock()
	d.feat = feats
	d.Name = name
	d.cids = cids
	d.diverted = map[uint16]byte{}
	e.mu.Unlock()
	logf("[%s] %q features=%v controls=%d", d.key, name, featList(feats), len(cids))

	e.readBattery(d)
	e.readDPI(d)
	e.readSmartShift(d)
	e.readWheels(d)
	e.readHosts(d)

	e.mu.Lock()
	d.Online = true
	d.Ready = true
	d.failures = 0
	e.mu.Unlock()
	e.applyDevice(d, true)
	e.changed()
}

func featList(m map[uint16]byte) string {
	var s []string
	for f := range m {
		s = append(s, fmt.Sprintf("%04X", f))
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

func readName(ch *Channel, dev, fi byte) string {
	p, err := ch.Call(dev, fi, 0)
	if err != nil {
		return ""
	}
	n := int(p[0])
	var b []byte
	for len(b) < n && len(b) < 64 {
		q, err := ch.Call(dev, fi, 1, byte(len(b)))
		if err != nil {
			break
		}
		chunk := q
		if rem := n - len(b); rem < len(chunk) {
			chunk = chunk[:rem]
		}
		b = append(b, chunk...)
	}
	return strings.TrimRight(string(b), "\x00 ")
}

func (e *Engine) setOnline(d *Device, on bool) {
	e.mu.Lock()
	was := d.Online
	d.Online = on
	if !on {
		d.Ready = false
	}
	e.mu.Unlock()
	if !on {
		e.releaseAll(d)
	}
	if was != on {
		logf("[%s] online=%v", d.key, on)
		e.changed()
	}
}

// releaseAll lets go of anything a button is still holding down.
// Must be called without e.mu held (releases may talk to the device).
func (e *Engine) releaseAll(d *Device) {
	e.mu.Lock()
	var rels []func()
	for cid, rel := range d.held {
		if rel != nil {
			rels = append(rels, rel)
		}
		delete(d.held, cid)
	}
	d.pressed = map[uint16]bool{}
	d.gesture = nil
	e.mu.Unlock()
	for _, r := range rels {
		r()
	}
}

func (e *Engine) readBattery(d *Device) {
	if fi, ok := d.feat[featUnifiedBatt]; ok {
		if p, err := d.ch.Call(d.idx, fi, 1); err == nil {
			e.setBattery(d, int(p[0]), p[2] == 1 || p[2] == 2)
			return
		}
	}
	if fi, ok := d.feat[featBattery]; ok {
		if p, err := d.ch.Call(d.idx, fi, 0); err == nil {
			e.setBattery(d, int(p[0]), p[2] >= 1 && p[2] <= 4 && p[2] != 3)
		}
	}
}

func (e *Engine) setBattery(d *Device, pct int, charging bool) {
	e.mu.Lock()
	d.Battery = pct
	d.Charging = charging
	alert := false
	if pct > 20 || charging {
		d.lowAlert = false
	} else if pct > 0 && pct <= 15 && !d.lowAlert && e.cfg.LowBatteryAlert {
		d.lowAlert = true
		alert = true
	}
	name := d.Name
	lang := e.cfg.Lang
	e.mu.Unlock()
	if alert && e.OnNotify != nil {
		if lang == "ar" {
			e.OnNotify("البطارية ضعيفة", fmt.Sprintf("%s: باقي %d%%، اشحنه قريباً", name, pct))
		} else {
			e.OnNotify("Low battery", fmt.Sprintf("%s is at %d%%, charge it soon", name, pct))
		}
	}
	e.changed()
}

func (e *Engine) readDPI(d *Device) {
	fi, ok := d.feat[featAdjustableDPI]
	if !ok {
		return
	}
	min, max, step := 200, 8000, 50
	if p, err := d.ch.Call(d.idx, fi, 1, 0); err == nil {
		var vals []int
		stepSeen := 0
		for i := 1; i+1 < len(p); i += 2 {
			v := int(be16(p[i : i+2]))
			if v == 0 {
				break
			}
			if v&0xE000 == 0xE000 {
				stepSeen = v & 0x1FFF
				continue
			}
			vals = append(vals, v)
		}
		if len(vals) > 0 {
			min, max = vals[0], vals[len(vals)-1]
			if stepSeen > 0 {
				step = stepSeen
			} else if len(vals) > 1 {
				step = vals[1] - vals[0]
			}
		}
	}
	cur := 0
	if p, err := d.ch.Call(d.idx, fi, 2, 0); err == nil {
		cur = int(be16(p[1:3]))
	}
	e.mu.Lock()
	d.DPIMin, d.DPIMax, d.DPIStep = min, max, step
	if !d.tempDPI {
		d.DPI = cur
	}
	e.mu.Unlock()
}

func (e *Engine) writeDPI(d *Device, dpi int) error {
	fi, ok := d.feat[featAdjustableDPI]
	if !ok {
		return errors.New("no DPI feature")
	}
	if d.DPIMax > 0 {
		if dpi < d.DPIMin {
			dpi = d.DPIMin
		}
		if dpi > d.DPIMax {
			dpi = d.DPIMax
		}
		if d.DPIStep > 0 {
			dpi = d.DPIMin + (dpi-d.DPIMin+d.DPIStep/2)/d.DPIStep*d.DPIStep
		}
	}
	_, err := d.ch.Call(d.idx, fi, 3, 0, byte(dpi>>8), byte(dpi))
	if err == nil {
		e.mu.Lock()
		d.DPI = dpi
		e.mu.Unlock()
	}
	return err
}

func (e *Engine) readSmartShift(d *Device) {
	if fi, ok := d.feat[featSmartShiftEnh]; ok {
		if p, err := d.ch.Call(d.idx, fi, 1); err == nil {
			e.mu.Lock()
			d.ssEnh, d.ssMode, d.ssAuto, d.ssTorque = true, p[0], p[1], p[2]
			e.mu.Unlock()
		}
		return
	}
	if fi, ok := d.feat[featSmartShift]; ok {
		if p, err := d.ch.Call(d.idx, fi, 0); err == nil {
			e.mu.Lock()
			d.ssEnh, d.ssMode, d.ssAuto = false, p[0], p[1]
			e.mu.Unlock()
		}
	}
}

func (e *Engine) writeSmartShift(d *Device, mode, auto, torque byte) error {
	var err error
	if fi, ok := d.feat[featSmartShiftEnh]; ok {
		_, err = d.ch.Call(d.idx, fi, 2, mode, auto, torque)
	} else if fi, ok := d.feat[featSmartShift]; ok {
		_, err = d.ch.Call(d.idx, fi, 1, mode, auto, 0)
	} else {
		return errors.New("no SmartShift")
	}
	if err == nil {
		e.readSmartShift(d)
	}
	return err
}

func (e *Engine) readWheels(d *Device) {
	if fi, ok := d.feat[featHiResWheel]; ok {
		if p, err := d.ch.Call(d.idx, fi, 1); err == nil {
			e.mu.Lock()
			d.hiresFlags = p[0]
			e.mu.Unlock()
		}
	}
	if fi, ok := d.feat[featThumbWheel]; ok {
		native, div := 18, 120
		if p, err := d.ch.Call(d.idx, fi, 0); err == nil {
			if n := int(be16(p[0:2])); n > 0 {
				native = n
			}
			if v := int(be16(p[2:4])); v > 0 {
				div = v
			}
		}
		e.mu.Lock()
		d.thumbNative, d.thumbDivRes = native, div
		e.mu.Unlock()
		if p, err := d.ch.Call(d.idx, fi, 1); err == nil {
			e.mu.Lock()
			d.thumbDiverted = p[0] != 0
			d.thumbInvert = p[1] != 0
			e.mu.Unlock()
		}
	}
}

func (e *Engine) readHosts(d *Device) {
	if fi, ok := d.feat[featChangeHost]; ok {
		if p, err := d.ch.Call(d.idx, fi, 0); err == nil {
			e.mu.Lock()
			d.Hosts, d.CurHost = int(p[0]), int(p[1])
			e.mu.Unlock()
		}
	}
}

// applyDevice pushes the configuration to one device. force re-sends diversions.
func (e *Engine) applyDevice(d *Device, force bool) {
	e.mu.Lock()
	cfg := e.cfg.Clone()
	e.mu.Unlock()
	if !d.Ready {
		return
	}
	// Pointer speed
	if cfg.DPI > 0 && !d.tempDPI && d.DPI != cfg.DPI {
		if err := e.writeDPI(d, cfg.DPI); err != nil {
			logf("[%s] set DPI: %v", d.key, err)
		}
	}
	// SmartShift
	if s := cfg.SmartShift; s != nil && (d.feat[featSmartShift] != 0 || d.feat[featSmartShiftEnh] != 0) {
		var mode, auto byte
		switch s.Mode {
		case "free":
			mode, auto = 1, 0
		case "ratchet":
			mode, auto = 2, 0xFF
		default:
			sens := s.Sensitivity
			if sens < 1 {
				sens = 1
			}
			if sens > 50 {
				sens = 50
			}
			mode, auto = 2, byte(sens)
		}
		torque := byte(0)
		if s.Torque > 0 && d.ssEnh {
			t := s.Torque
			if t > 100 {
				t = 100
			}
			torque = byte(t)
		}
		wantAuto := auto
		if mode == 1 {
			wantAuto = d.ssAuto
		}
		if force || d.ssMode != mode || d.ssAuto != wantAuto || (torque != 0 && d.ssTorque != torque) {
			if err := e.writeSmartShift(d, mode, auto, torque); err != nil {
				logf("[%s] set SmartShift: %v", d.key, err)
			}
		}
	}
	// Main wheel
	if fi, ok := d.feat[featHiResWheel]; ok && (cfg.SmoothScroll != nil || cfg.InvertScroll != nil) {
		f := d.hiresFlags &^ 0x01 // never divert the main wheel
		if cfg.SmoothScroll != nil {
			if *cfg.SmoothScroll {
				f |= 0x02
			} else {
				f &^= 0x02
			}
		}
		if cfg.InvertScroll != nil {
			if *cfg.InvertScroll {
				f |= 0x04
			} else {
				f &^= 0x04
			}
		}
		if f != d.hiresFlags || force {
			if _, err := d.ch.Call(d.idx, fi, 2, f); err != nil {
				logf("[%s] set wheel mode: %v", d.key, err)
			} else {
				e.mu.Lock()
				d.hiresFlags = f
				e.mu.Unlock()
			}
		}
	}
	// Thumb wheel
	if fi, ok := d.feat[featThumbWheel]; ok {
		div := !cfg.Paused && thumbNeedsDivert(cfg)
		inv := d.thumbInvert
		if cfg.InvertThumb != nil {
			inv = *cfg.InvertThumb
		}
		if force || div != d.thumbDiverted || inv != d.thumbInvert {
			if _, err := d.ch.Call(d.idx, fi, 2, b2b(div), b2b(inv)); err != nil {
				logf("[%s] set thumb wheel: %v", d.key, err)
			} else {
				e.mu.Lock()
				d.thumbDiverted, d.thumbInvert = div, inv
				e.mu.Unlock()
			}
		}
	}
	// Buttons
	e.applyDiversions(d, cfg, force)
	e.changed()
}

func b2b(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func thumbNeedsDivert(cfg *Config) bool {
	if cfg.Thumb.Type != "default" {
		return true
	}
	for _, p := range cfg.Profiles {
		if p.Thumb.Type != "" && p.Thumb.Type != "inherit" && p.Thumb.Type != "default" {
			return true
		}
	}
	return false
}

// wantedDivert returns the setCidReporting bit field we want for a button key.
func wantedDivert(cfg *Config, key string, info cidInfo) byte {
	if cfg.Paused || !info.Divertable {
		return 0x02 // not diverted
	}
	acts := []Action{cfg.Buttons[key]}
	for _, p := range cfg.Profiles {
		if a, ok := p.Buttons[key]; ok && a.Type != "inherit" && a.Type != "" {
			acts = append(acts, a)
		}
	}
	divert, raw := false, false
	for _, a := range acts {
		if a.Type != "default" {
			divert = true
		}
		if a.Type == "gestures" {
			raw = true
		}
	}
	if !divert {
		return 0x02
	}
	if raw && info.RawXY {
		return 0x33
	}
	return 0x03
}

func (e *Engine) applyDiversions(d *Device, cfg *Config, force bool) {
	fi, ok := d.feat[featReprogV4]
	if !ok {
		return
	}
	for _, key := range buttonKeys {
		cid, info, ok := d.cidFor(key)
		if !ok {
			continue
		}
		want := wantedDivert(cfg, key, info)
		have, known := d.diverted[cid]
		if !force && known && have == want {
			continue
		}
		if !known && want == 0x02 && !force {
			continue
		}
		bf := want
		if want == 0x02 && have == 0x33 {
			bf = 0x22 // also switch raw XY off
		}
		if want == 0x03 && have == 0x33 {
			bf = 0x23
		}
		_, err := d.ch.Call(d.idx, fi, 3, byte(cid>>8), byte(cid), bf, 0, 0)
		if err != nil && want == 0x33 {
			// Raw XY refused: fall back to plain button diversion.
			want, bf = 0x03, 0x03
			_, err = d.ch.Call(d.idx, fi, 3, byte(cid>>8), byte(cid), bf, 0, 0)
		}
		if err != nil {
			logf("[%s] divert %s (0x%04X): %v", d.key, key, cid, err)
			continue
		}
		e.mu.Lock()
		d.diverted[cid] = want
		e.mu.Unlock()
		logf("[%s] %s -> %s", d.key, key, map[byte]string{0x02: "native", 0x03: "diverted", 0x33: "diverted+gesture"}[want])
	}
}

func (e *Engine) undivertAll(d *Device) {
	fi, ok := d.feat[featReprogV4]
	if ok {
		for cid, bf := range d.diverted {
			if bf == 0x02 {
				continue
			}
			_, _ = d.ch.Call(d.idx, fi, 3, byte(cid>>8), byte(cid), 0x22, 0, 0)
		}
	}
	if fi, ok := d.feat[featThumbWheel]; ok && d.thumbDiverted {
		_, _ = d.ch.Call(d.idx, fi, 2, 0, b2b(d.thumbInvert))
	}
	if d.tempDPI && d.savedDPI > 0 {
		_ = e.writeDPI(d, d.savedDPI)
	}
}

// ---------- watchdog ----------

func (e *Engine) watchdog(full bool) {
	e.mu.Lock()
	var devs []*Device
	for _, d := range e.devices {
		devs = append(devs, d)
	}
	e.mu.Unlock()
	for _, d := range devs {
		if !d.Ready {
			// Directly connected devices never send connection events; poll them.
			// Receiver devices are polled too in case an event was missed.
			if time.Since(d.lastInit) > 8*time.Second {
				e.initDevice(d)
			}
			continue
		}
		fi, ok := d.feat[featReprogV4]
		var probe uint16
		for cid, bf := range d.diverted {
			if bf != 0x02 {
				probe = cid
				break
			}
		}
		if ok && probe != 0 {
			p, err := d.ch.Call(d.idx, fi, 2, byte(probe>>8), byte(probe))
			if err != nil {
				d.failures++
				if d.failures >= 3 {
					e.setOnline(d, false)
				}
				continue
			}
			d.failures = 0
			if p[2]&0x01 == 0 {
				logf("[%s] settings were lost (mouse slept or reconnected) - applying again", d.key)
				e.readDPI(d)
				e.applyDevice(d, true)
				continue
			}
		}
		if full {
			e.readBattery(d)
		}
	}
}

// ---------- events ----------

func (e *Engine) handlePacket(ls *linkState, pkt []byte) {
	if len(pkt) < 7 {
		return
	}
	idx, sub := pkt[1], pkt[2]
	if ls.kind == "receiver" && e.handlePairingPacket(ls, pkt) {
		return
	}
	// Receiver: device connected / disconnected.
	if sub == 0x41 && ls.kind == "receiver" {
		linked := pkt[4]&0x40 == 0
		wasSeen := ls.seen[idx]
		ls.seen[idx] = true
		if linked && !wasSeen {
			e.unifyingPaired(ls, idx)
		}
		d := e.device(ls, idx)
		d.WPID = uint16(pkt[6])<<8 | uint16(pkt[5])
		logf("[%s] connection event: linked=%v wpid=%04X", d.key, linked, d.WPID)
		if !linked {
			e.setOnline(d, false)
			return
		}
		gen := d.initGen
		e.postAfter(400*time.Millisecond, func() {
			if d.initGen == gen && !(d.Ready && time.Since(d.lastInit) < 2*time.Second) {
				e.initDevice(d)
			}
		})
		return
	}
	if pkt[0] == reportShort {
		return
	}
	key := fmt.Sprintf("%s#%d", ls.id, idx)
	if ls.kind == "direct" {
		key = fmt.Sprintf("%s#%d", ls.id, devReceiver)
	}
	e.mu.Lock()
	d := e.devices[key]
	e.mu.Unlock()
	if d == nil || !d.Ready {
		return
	}
	fn := pkt[3] >> 4
	if sub == 0 || pkt[3]&0x0F != 0 {
		return // an answer to someone else's request
	}
	p := pkt[4:]
	switch sub {
	case d.feat[featReprogV4]:
		if fn == 0 {
			e.onButtons(d, p)
		} else if fn == 1 && d.gesture != nil {
			d.gesture.dx += int(sbe16(p[0:2]))
			d.gesture.dy += int(sbe16(p[2:4]))
		}
	case d.feat[featThumbWheel]:
		if fn == 0 {
			e.onThumb(d, int(sbe16(p[0:2])), p[4])
		}
	case d.feat[featUnifiedBatt]:
		if fn == 0 {
			e.setBattery(d, int(p[0]), p[2] == 1 || p[2] == 2)
		}
	case d.feat[featBattery]:
		if fn == 0 {
			e.setBattery(d, int(p[0]), p[2] >= 1 && p[2] <= 4 && p[2] != 3)
		}
	case d.feat[featSmartShift], d.feat[featSmartShiftEnh]:
		e.readSmartShift(d)
		e.changed()
	}
}

// cidFor finds which control on this device plays the role of a button.
func (d *Device) cidFor(key string) (uint16, cidInfo, bool) {
	for _, cid := range buttonCIDs[key] {
		if info, ok := d.cids[cid]; ok && info.Divertable {
			return cid, info, true
		}
	}
	return 0, cidInfo{}, false
}

func (d *Device) keyOf(cid uint16) string {
	for _, k := range buttonKeys {
		if c, _, ok := d.cidFor(k); ok && c == cid {
			return k
		}
	}
	for k, c := range buttonCID {
		if c == cid {
			return k
		}
	}
	return ""
}

func (e *Engine) foreground() string {
	if e.fgFunc == nil {
		return ""
	}
	e.fgMu.Lock()
	defer e.fgMu.Unlock()
	if time.Since(e.fgCacheAt) < 250*time.Millisecond {
		return e.fgCache
	}
	e.fgCache = strings.ToLower(e.fgFunc())
	e.fgCacheAt = time.Now()
	return e.fgCache
}

// resolve finds the action for a button in the current app.
func (e *Engine) resolve(key string) Action {
	fg := e.foreground()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.cfg.Profiles {
		if fg != "" && p.Exe == fg {
			if a, ok := p.Buttons[key]; ok && a.Type != "inherit" && a.Type != "" {
				return a
			}
		}
	}
	return e.cfg.Buttons[key]
}

func (e *Engine) resolveThumb() WheelAction {
	fg := e.foreground()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.cfg.Profiles {
		if fg != "" && p.Exe == fg && p.Thumb.Type != "inherit" && p.Thumb.Type != "" {
			return p.Thumb
		}
	}
	return e.cfg.Thumb
}

func (e *Engine) onButtons(d *Device, p []byte) {
	now := map[uint16]bool{}
	for i := 0; i+1 < 8; i += 2 {
		if c := be16(p[i : i+2]); c != 0 {
			now[c] = true
		}
	}
	for cid := range now {
		if !d.pressed[cid] {
			e.buttonDown(d, cid)
		}
	}
	for cid := range d.pressed {
		if !now[cid] {
			e.buttonUp(d, cid)
		}
	}
	d.pressed = now
}

type devOps struct {
	e *Engine
	d *Device
}

func (o devOps) ToggleSmartShift() { o.e.toggleSmartShift(o.d) }
func (o devOps) CycleDPI()         { o.e.cycleDPI(o.d) }
func (o devOps) PrecisionDPI() int {
	o.e.mu.Lock()
	defer o.e.mu.Unlock()
	return o.e.cfg.PrecisionDPI
}
func (o devOps) SetTempDPI(dpi int) {
	d, e := o.d, o.e
	if d.ch == nil { // any mouse: slow Windows down instead
		e.tempSysSpeed(dpi > 0)
		return
	}
	if dpi > 0 {
		if !d.tempDPI {
			d.savedDPI = d.DPI
		}
		if err := e.writeDPI(d, dpi); err == nil {
			d.tempDPI = true
		}
	} else if d.tempDPI {
		d.tempDPI = false
		_ = e.writeDPI(d, d.savedDPI)
	}
	e.changed()
}

func (e *Engine) buttonDown(d *Device, cid uint16) {
	key := d.keyOf(cid)
	act := e.resolve(key)
	logf("[%s] %s down -> %s %s", d.key, key, act.Type, act.Value)
	switch act.Type {
	case "gestures":
		if act.Gestures != nil {
			d.gesture = &gestureState{cid: cid, g: act.Gestures}
		}
	case "default":
		// The button is diverted because another app profile needs it:
		// recreate what it does natively.
		switch key {
		case "back", "forward", "middle":
			e.act.MouseButton(key, true)
			d.held[cid] = func() { e.act.MouseButton(key, false) }
		case "top":
			e.toggleSmartShift(d)
		case "dpiswitch":
			e.cycleDPI(d)
		}
	case "none":
	default:
		if rel := runAction(act, e.act, devOps{e, d}); rel != nil {
			d.held[cid] = rel
		}
	}
}

func (e *Engine) buttonUp(d *Device, cid uint16) {
	if g := d.gesture; g != nil && g.cid == cid {
		d.gesture = nil
		e.mu.Lock()
		thr := e.cfg.GestureDistance
		e.mu.Unlock()
		ax, ay := abs(g.dx), abs(g.dy)
		var a Action
		dir := "click"
		switch {
		case ax < thr && ay < thr:
			a = g.g.Click
		case ax >= ay && g.dx < 0:
			a, dir = g.g.Left, "left"
		case ax >= ay:
			a, dir = g.g.Right, "right"
		case g.dy < 0:
			a, dir = g.g.Up, "up"
		default:
			a, dir = g.g.Down, "down"
		}
		logf("[%s] gesture %s (dx=%d dy=%d) -> %s %s", d.key, dir, g.dx, g.dy, a.Type, a.Value)
		if rel := runAction(a, e.act, devOps{e, d}); rel != nil {
			rel()
		}
		return
	}
	if rel := d.held[cid]; rel != nil {
		delete(d.held, cid)
		rel()
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (e *Engine) onThumb(d *Device, rot int, status byte) {
	if rot == 0 {
		if status == 3 || status == 0 {
			d.thumbAcc = 0
		}
		return
	}
	w := e.resolveThumb()
	if w.Type == "default" || w.Type == "hscroll" {
		// Smooth horizontal scrolling, same speed as the native wheel.
		num := rot*120*d.thumbNative + d.thumbRem
		den := d.thumbDivRes
		if den <= 0 {
			den = 120
		}
		delta := num / den
		d.thumbRem = num - delta*den
		if delta != 0 {
			e.act.Wheel(delta, true)
		}
		return
	}
	e.mu.Lock()
	sens := e.cfg.ThumbSensitivity
	e.mu.Unlock()
	notches := 6 + sens*2
	thr := d.thumbDivRes / notches
	if thr < 1 {
		thr = 1
	}
	d.thumbAcc += rot
	for d.thumbAcc >= thr {
		d.thumbAcc -= thr
		thumbStep(w, 1, e.act)
	}
	for d.thumbAcc <= -thr {
		d.thumbAcc += thr
		thumbStep(w, -1, e.act)
	}
}

func (e *Engine) toggleSmartShift(d *Device) {
	e.readSmartShift(d)
	if d.ssMode == 2 {
		_ = e.writeSmartShift(d, 1, 0, 0)
	} else {
		_ = e.writeSmartShift(d, 2, 0, 0)
	}
	e.changed()
}

func (e *Engine) cycleDPI(d *Device) {
	e.mu.Lock()
	presets := append([]int(nil), e.cfg.DPIPresets...)
	e.mu.Unlock()
	if len(presets) == 0 {
		return
	}
	sort.Ints(presets)
	next := presets[0]
	for _, p := range presets {
		if p > d.DPI {
			next = p
			break
		}
	}
	if err := e.writeDPI(d, next); err == nil {
		e.mu.Lock()
		e.cfg.DPI = d.DPI
		cfg := e.cfg.Clone()
		e.mu.Unlock()
		_ = e.store.Save(cfg)
		lang := cfg.Lang
		if e.OnNotify != nil {
			if lang == "ar" {
				e.OnNotify("سرعة الماوس", fmt.Sprintf("%d DPI", d.DPI))
			} else {
				e.OnNotify("Pointer speed", fmt.Sprintf("%d DPI", d.DPI))
			}
		}
		e.changed()
	}
}

// ---------- API used by the UI ----------

func (e *Engine) Config() *Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.Clone()
}

func (e *Engine) SetConfig(c *Config) error {
	c.normalize()
	if err := e.store.Save(c); err != nil {
		return err
	}
	e.mu.Lock()
	old := e.cfg
	e.cfg = c.Clone()
	e.mu.Unlock()
	e.applySystem(old, c)
	e.post(func() {
		e.mu.Lock()
		var devs []*Device
		for _, d := range e.devices {
			devs = append(devs, d)
		}
		e.mu.Unlock()
		for _, d := range devs {
			if d.Ready {
				e.applyDevice(d, false)
			}
		}
	})
	return nil
}

func (e *Engine) SwitchHost(key string, host int) {
	e.post(func() {
		e.mu.Lock()
		d := e.devices[key]
		e.mu.Unlock()
		if d == nil || !d.Ready {
			return
		}
		if fi, ok := d.feat[featChangeHost]; ok {
			old := d.ch.Timeout
			d.ch.Timeout = 300 * time.Millisecond
			_, _ = d.ch.Call(d.idx, fi, 1, byte(host))
			d.ch.Timeout = old
		}
	})
}

func (e *Engine) Refresh() {
	e.post(func() {
		e.mu.Lock()
		var devs []*Device
		for _, d := range e.devices {
			devs = append(devs, d)
		}
		e.mu.Unlock()
		for _, d := range devs {
			e.initDevice(d)
		}
	})
}

type DeviceView struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Conn         string   `json:"conn"`
	Online       bool     `json:"online"`
	Ready        bool     `json:"ready"`
	Mouse        bool     `json:"mouse"`
	Battery      int      `json:"battery"`
	Charging     bool     `json:"charging"`
	DPI          int      `json:"dpi"`
	DPIMin       int      `json:"dpiMin"`
	DPIMax       int      `json:"dpiMax"`
	DPIStep      int      `json:"dpiStep"`
	HasDPI       bool     `json:"hasDpi"`
	HasSS        bool     `json:"hasSmartShift"`
	HasTorque    bool     `json:"hasTorque"`
	SSMode       string   `json:"ssMode"`
	SSSens       int      `json:"ssSensitivity"`
	SSTorque     int      `json:"ssTorque"`
	HasHiRes     bool     `json:"hasHiRes"`
	SmoothScroll bool     `json:"smoothScroll"`
	InvertScroll bool     `json:"invertScroll"`
	HasThumb     bool     `json:"hasThumb"`
	InvertThumb  bool     `json:"invertThumb"`
	Hosts        int      `json:"hosts"`
	CurHost      int      `json:"curHost"`
	Buttons      []string `json:"buttons"`
	Gestures     bool     `json:"gestures"`
	Link         string   `json:"link"`
	Generic      bool     `json:"generic"`
	Slot         int      `json:"slot"`
}

func (e *Engine) Devices() []DeviceView {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []DeviceView
	for _, d := range e.devices {
		v := DeviceView{Key: d.key, Name: d.Name, Conn: d.Conn, Online: d.Online, Ready: d.Ready,
			Battery: d.Battery, Charging: d.Charging, DPI: d.DPI, DPIMin: d.DPIMin, DPIMax: d.DPIMax, DPIStep: d.DPIStep,
			Hosts: d.Hosts, CurHost: d.CurHost, Slot: int(d.idx)}
		if d.link != nil {
			v.Link = d.link.id
		}
		_, v.HasDPI = d.feat[featAdjustableDPI]
		_, hasRe := d.feat[featReprogV4]
		_, v.HasHiRes = d.feat[featHiResWheel]
		_, v.HasThumb = d.feat[featThumbWheel]
		v.Mouse = v.HasDPI || v.HasHiRes || (d.Ready == false && d.Name == "")
		v.HasSS = d.feat[featSmartShift] != 0 || d.feat[featSmartShiftEnh] != 0
		v.HasTorque = d.ssEnh
		switch {
		case d.ssMode == 1:
			v.SSMode = "free"
		case d.ssAuto == 0xFF:
			v.SSMode = "ratchet"
		default:
			v.SSMode = "smart"
		}
		v.SSSens = int(d.ssAuto)
		v.SSTorque = int(d.ssTorque)
		v.SmoothScroll = d.hiresFlags&0x02 != 0
		v.InvertScroll = d.hiresFlags&0x04 != 0
		v.InvertThumb = d.thumbInvert
		if hasRe {
			for _, k := range buttonKeys {
				if _, info, ok := d.cidFor(k); ok {
					v.Buttons = append(v.Buttons, k)
					if k == "gesture" {
						v.Gestures = info.RawXY
					}
				}
			}
		}
		if v.Name == "" {
			v.Name = "Logitech"
		}
		out = append(out, v)
	}
	if g := e.generic; g != nil {
		out = append(out, DeviceView{Key: g.key, Name: g.Name, Conn: "system", Online: true, Ready: true, Mouse: true,
			Battery: -1, Buttons: []string{"back", "forward", "middle"}, Gestures: true, Generic: true})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generic != out[j].Generic {
			return !out[i].Generic
		}
		if out[i].Mouse != out[j].Mouse {
			return out[i].Mouse
		}
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Summary for the tray tooltip.
func (e *Engine) Summary() (name string, battery int, online bool) {
	for _, v := range e.Devices() {
		if v.Mouse {
			return v.Name, v.Battery, v.Online
		}
	}
	return "", -1, false
}
