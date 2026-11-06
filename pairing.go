package main

// First-time pairing with a Logi Bolt receiver (and forgetting paired devices).
// Protocol as used by Solaar: register 0xC0 starts discovery, long register 0x2C1
// pairs/unpairs, and the receiver reports progress with notifications
// 0x53 (discovery status), 0x4F (device found), 0x54 (pairing status),
// 0x4D (passkey request) and 0x4E (passkey key pressed).

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PairingView struct {
	Active   bool     `json:"active"`
	Link     string   `json:"link"`
	Stage    string   `json:"stage"` // discovering | pairing | passkey | done | failed
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // mouse | keyboard | other
	Passkey  string   `json:"passkey"`
	Clicks   []string `json:"clicks"` // mouse passkey: left/right sequence
	Keyboard bool     `json:"keyboard"`
	Pressed  int      `json:"pressed"`
	Error    string   `json:"error"`
	Unifying bool     `json:"unifying"`
}

type pairingState struct {
	PairingView
	ls       *linkState
	address  []byte
	auth     byte
	kindByte byte
	counter  int
	sentPair bool
	gen      int
}

type ReceiverView struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // bolt | unifying
	Name    string `json:"name"`
	Devices int    `json:"devices"`
}

const pairTimeoutSec = 40

// StartPairing puts a Bolt receiver into discovery mode.
func (e *Engine) StartPairing(linkID string) {
	e.post(func() {
		e.mu.Lock()
		ls := e.links[linkID]
		if ls != nil && ls.kind != "receiver" {
			ls = nil
		}
		if ls == nil {
			for _, l := range e.links {
				if l.kind == "receiver" && (ls == nil || isBolt(l)) {
					ls = l
				}
			}
		}
		if e.pair != nil && e.pair.Active {
			e.mu.Unlock()
			return
		}
		gen := 1
		if e.pair != nil {
			gen = e.pair.gen + 1
		}
		if ls == nil {
			e.pair = &pairingState{PairingView: PairingView{Active: false, Stage: "failed", Error: "noreceiver"}, gen: gen}
			e.mu.Unlock()
			e.changed()
			return
		}
		bolt := isBolt(ls)
		stage := "discovering"
		if !bolt {
			stage = "waiting"
		}
		e.pair = &pairingState{PairingView: PairingView{Active: true, Link: ls.id, Stage: stage, Unifying: !bolt}, ls: ls, counter: -1, gen: gen}
		e.mu.Unlock()
		if !bolt {
			// Unifying-style receivers: open the pairing lock, then the device pairs
			// itself when it is switched off and on again.
			logf("pairing: opening the pairing lock on %s", ls.id)
			if _, err := ls.ch.Register(devReceiver, 0x80, 0xB2, 0x01, 0x00, pairTimeoutSec); err != nil {
				logf("pairing: lock did not open: %v", err)
				e.pairFail("start")
				return
			}
			e.changed()
			e.postAfter((pairTimeoutSec+5)*time.Second, func() {
				if e.pair != nil && e.pair.gen == gen && e.pair.Active {
					e.CancelPairingNow()
					e.pairFail("notfound")
				}
			})
			return
		}
		logf("pairing: starting discovery on %s", ls.id)
		if _, err := ls.ch.Register(devReceiver, 0x80, 0xC0, pairTimeoutSec, 0x01, 0x00); err != nil {
			logf("pairing: discovery did not start: %v", err)
			e.pairFail("start")
			return
		}
		e.changed()
		e.postAfter((pairTimeoutSec+20)*time.Second, func() {
			if e.pair != nil && e.pair.gen == gen && e.pair.Active {
				e.CancelPairingNow()
				e.pairFail("timeout")
			}
		})
	})
}

func isBolt(ls *linkState) bool {
	p := strings.ToLower(ls.product)
	return strings.Contains(p, "bolt") || strings.Contains(p, "c548")
}

func (e *Engine) CancelPairing() { e.post(e.CancelPairingNow) }

// CancelPairingNow must run on the worker.
func (e *Engine) CancelPairingNow() {
	p := e.pair
	if p == nil || p.ls == nil {
		return
	}
	if p.Active {
		if p.Unifying {
			_, _ = p.ls.ch.Register(devReceiver, 0x80, 0xB2, 0x02, 0x00, 0x00)
		} else {
			_, _ = p.ls.ch.RegisterLong(devReceiver, 0xC1, 0x02)
			_, _ = p.ls.ch.Register(devReceiver, 0x80, 0xC0, pairTimeoutSec, 0x02, 0x00)
		}
		logf("pairing: cancelled")
	}
	e.mu.Lock()
	p.Active = false
	if p.Stage != "done" && p.Stage != "failed" {
		p.Stage = "cancelled"
	}
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) ClearPairing() {
	e.post(func() {
		if e.pair != nil && e.pair.Active {
			e.CancelPairingNow()
		}
		e.mu.Lock()
		e.pair = nil
		e.mu.Unlock()
		e.changed()
	})
}

func (e *Engine) pairFail(reason string) {
	e.mu.Lock()
	if e.pair != nil {
		e.pair.Active = false
		e.pair.Stage = "failed"
		if e.pair.Error == "" {
			e.pair.Error = reason
		}
	}
	e.mu.Unlock()
	logf("pairing failed: %s", reason)
	e.changed()
}

// handlePairingPacket returns true when the packet was a pairing notification.
func (e *Engine) handlePairingPacket(ls *linkState, pkt []byte) bool {
	if pkt[1] != devReceiver || len(pkt) < 7 {
		return false
	}
	sub := pkt[2]
	if sub == 0x4A { // Unifying pairing lock status
		p := e.pair
		if p == nil || p.ls != ls || !p.Unifying {
			return true
		}
		open := pkt[3]&0x01 != 0
		if errc := pkt[4]; errc != 0 {
			e.pairFail(map[byte]string{1: "timeout", 2: "failed", 3: "failed", 6: "full"}[errc])
		} else if !open && p.Active && p.Stage == "waiting" {
			e.pairFail("notfound")
		}
		return true
	}
	if sub != 0x4D && sub != 0x4E && sub != 0x4F && sub != 0x53 && sub != 0x54 {
		return false
	}
	p := e.pair
	if p == nil || p.ls != ls {
		return true
	}
	at := func(i int) byte {
		if i < len(pkt) {
			return pkt[i]
		}
		return 0
	}
	switch sub {
	case 0x53: // discovery status: address 0 = discovering
		on := pkt[3] == 0x00
		if errc := at(4); errc != 0 {
			e.pairFail(map[byte]string{1: "timeout", 2: "failed"}[errc])
			return true
		}
		if !on && p.Stage == "discovering" && !p.sentPair {
			e.pairFail("notfound")
		}
	case 0x4F: // a device in pairing mode was found
		counter := int(pkt[3]) + int(at(4))*256
		if p.counter >= 0 && p.counter != counter {
			return true
		}
		p.counter = counter
		switch at(5) {
		case 0:
			p.kindByte = at(7)
			if len(pkt) >= 16 {
				p.address = append([]byte(nil), pkt[10:16]...)
			}
			p.auth = at(18)
		case 1:
			n := int(at(6))
			if 7+n <= len(pkt) {
				e.mu.Lock()
				p.Name = strings.TrimRight(string(pkt[7:7+n]), "\x00")
				e.mu.Unlock()
			}
		}
		e.mu.Lock()
		p.Kind = map[byte]string{1: "keyboard", 2: "mouse"}[p.kindByte&0x0F]
		if p.Kind == "" {
			p.Kind = "other"
		}
		e.mu.Unlock()
		if p.address != nil && p.Name != "" && !p.sentPair {
			p.sentPair = true
			entropy := byte(10)
			if p.Kind == "keyboard" {
				entropy = 20
			}
			params := append([]byte{0x01, 0x00}, p.address...)
			params = append(params, p.auth, entropy)
			logf("pairing: found %q (%s), auth=%02X - pairing", p.Name, p.Kind, p.auth)
			e.mu.Lock()
			p.Stage = "pairing"
			e.mu.Unlock()
			if _, err := ls.ch.RegisterLong(devReceiver, 0xC1, params...); err != nil {
				logf("pairing: pair request: %v", err)
				e.pairFail("failed")
				return true
			}
		}
		e.changed()
	case 0x4D: // passkey to enter on the device
		code := strings.TrimRight(string(pkt[4:10]), "\x00 ")
		e.mu.Lock()
		p.Passkey = code
		p.Stage = "passkey"
		p.Keyboard = p.auth&0x01 != 0
		p.Clicks = nil
		if !p.Keyboard {
			if n, err := strconv.Atoi(code); err == nil {
				bits := fmt.Sprintf("%010b", n)
				for _, b := range bits {
					if b == '1' {
						p.Clicks = append(p.Clicks, "right")
					} else {
						p.Clicks = append(p.Clicks, "left")
					}
				}
			}
		}
		e.mu.Unlock()
		logf("pairing: passkey requested")
		e.changed()
	case 0x4E: // a passkey key/click was registered
		e.mu.Lock()
		switch pkt[3] { // 0 started, 1 entered, 2 erased, 3 cleared, 4 completed
		case 0x01:
			p.Pressed++
		case 0x02:
			if p.Pressed > 0 {
				p.Pressed--
			}
		case 0x00, 0x03:
			p.Pressed = 0
		case 0x04:
			p.Pressed = len(p.Clicks)
		}
		e.mu.Unlock()
		e.changed()
	case 0x54: // pairing status
		errc := at(4)
		if errc != 0 {
			e.pairFail(map[byte]string{1: "timeout", 2: "failed"}[errc])
			return true
		}
		if pkt[3] == 0x02 {
			idx := at(11)
			logf("pairing: done, new device in slot %d", idx)
			e.mu.Lock()
			p.Active = false
			p.Stage = "done"
			e.mu.Unlock()
			e.changed()
			if idx >= 1 && idx <= 6 {
				d := e.device(ls, idx)
				ls.seen[idx] = true
				e.postAfter(800*time.Millisecond, func() { e.initDevice(d) })
			}
		} else if pkt[3] == 0x01 && p.Stage != "done" && p.Active && p.sentPair {
			e.pairFail("failed")
		}
	}
	return true
}

// Unpair forgets a device on its receiver.
func (e *Engine) Unpair(key string) {
	e.post(func() {
		e.mu.Lock()
		d := e.devices[key]
		e.mu.Unlock()
		if d == nil || d.link == nil || d.link.kind != "receiver" {
			return
		}
		var err error
		if isBolt(d.link) {
			_, err = d.ch.RegisterLong(devReceiver, 0xC1, 0x03, d.idx)
		} else {
			_, err = d.ch.Register(devReceiver, 0x80, 0xB2, 0x03, d.idx, 0x00)
		}
		if err != nil {
			logf("[%s] unpair: %v", key, err)
			if e.OnNotify != nil {
				e.OnNotify("Mishk", err.Error())
			}
			return
		}
		logf("[%s] unpaired", key)
		e.releaseAll(d)
		e.mu.Lock()
		delete(e.devices, key)
		delete(d.link.seen, d.idx)
		e.mu.Unlock()
		e.changed()
	})
}

func (e *Engine) Pairing() *PairingView {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pair == nil {
		return nil
	}
	v := e.pair.PairingView
	v.Clicks = append([]string(nil), v.Clicks...)
	return &v
}

func (e *Engine) Receivers() []ReceiverView {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []ReceiverView
	for _, ls := range e.links {
		if ls.kind != "receiver" {
			continue
		}
		r := ReceiverView{ID: ls.id, Name: ls.product, Kind: "unifying"}
		if isBolt(ls) {
			r.Kind = "bolt"
		}
		for _, d := range e.devices {
			if d.link == ls {
				r.Devices++
			}
		}
		out = append(out, r)
	}
	return out
}

// unifyingPaired is called when a device appears in a slot we had not seen,
// which is how a Unifying receiver reports a new pairing.
func (e *Engine) unifyingPaired(ls *linkState, idx byte) {
	p := e.pair
	if p == nil || p.ls != ls || !p.Unifying || !p.Active {
		return
	}
	logf("pairing: new device in slot %d", idx)
	_, _ = ls.ch.Register(devReceiver, 0x80, 0xB2, 0x02, 0x00, 0x00) // close the lock
	e.mu.Lock()
	p.Active = false
	p.Stage = "done"
	e.mu.Unlock()
	e.changed()
	d := e.device(ls, idx)
	e.postAfter(900*time.Millisecond, func() {
		if !d.Ready {
			e.initDevice(d)
		}
		e.mu.Lock()
		if e.pair == p {
			p.Name = d.Name
		}
		e.mu.Unlock()
		e.changed()
	})
}
