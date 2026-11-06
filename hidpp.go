package main

// HID++ (Logitech) protocol layer. Platform independent: the raw HID handles
// live in hid_windows.go, and the tests drive this with a simulated mouse.

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	reportShort = 0x10 // 7 bytes
	reportLong  = 0x11 // 20 bytes
	reportVLong = 0x12 // 64 bytes

	swID = 0x0B // our software id (low nibble of byte 3)

	devReceiver = 0xFF // receiver itself, or a directly connected (Bluetooth) device
)

// HID++ 2.0 feature ids we care about.
const (
	featRoot          = 0x0000
	featDeviceName    = 0x0005
	featBattery       = 0x1000
	featUnifiedBatt   = 0x1004
	featChangeHost    = 0x1814
	featReprogV4      = 0x1B04
	featAdjustableDPI = 0x2201
	featSmartShift    = 0x2110
	featSmartShiftEnh = 0x2111
	featHiResWheel    = 0x2121
	featThumbWheel    = 0x2150
)

var hidpp20Errors = map[byte]string{
	0x01: "unknown", 0x02: "invalid argument", 0x03: "out of range",
	0x04: "hardware error", 0x05: "logitech internal", 0x06: "invalid feature index",
	0x07: "invalid function", 0x08: "busy", 0x09: "unsupported",
}

var hidpp10Errors = map[byte]string{
	0x01: "invalid sub id", 0x02: "invalid address", 0x03: "invalid value",
	0x04: "connect fail", 0x05: "too many devices", 0x06: "already exists",
	0x07: "busy", 0x08: "unknown device", 0x09: "resource error",
	0x0A: "request unavailable", 0x0B: "invalid param", 0x0C: "wrong pin",
}

// Error returned by the device.
type HidppError struct {
	V10  bool
	Code byte
}

func (e *HidppError) Error() string {
	if e.V10 {
		return fmt.Sprintf("HID++1.0 error 0x%02X (%s)", e.Code, hidpp10Errors[e.Code])
	}
	return fmt.Sprintf("HID++2.0 error 0x%02X (%s)", e.Code, hidpp20Errors[e.Code])
}

var ErrTimeout = errors.New("no answer from device (asleep or out of range?)")
var ErrClosed = errors.New("channel closed")

// Link is a raw transport: one physical receiver or Bluetooth device.
// Send gets a full report including the report id as first byte.
type Link interface {
	Send(report []byte) error
	HasShort() bool
	Close()
}

type reqKey struct{ dev, sub, addr byte }

// Channel multiplexes requests and notifications over a Link.
type Channel struct {
	link     Link
	mu       sync.Mutex
	pending  map[reqKey]chan []byte
	reqLock  sync.Mutex // one request in flight at a time keeps things simple and safe
	notify   func(pkt []byte)
	closed   bool
	Timeout  time.Duration
	Receiver bool // true when behind a Bolt/Unifying receiver
}

func NewChannel(link Link, receiver bool, notify func([]byte)) *Channel {
	return &Channel{link: link, pending: map[reqKey]chan []byte{}, notify: notify,
		Timeout: 1200 * time.Millisecond, Receiver: receiver}
}

// Deliver is called by the transport reader for every incoming report.
func (c *Channel) Deliver(pkt []byte) {
	if len(pkt) < 4 {
		return
	}
	switch pkt[0] {
	case reportShort, reportLong, reportVLong:
	default:
		return
	}
	dev, sub, addr := pkt[1], pkt[2], pkt[3]
	var key reqKey
	switch {
	case (sub == 0xFF || sub == 0x8F) && len(pkt) >= 6:
		key = reqKey{dev, pkt[3], pkt[4]}
	default:
		key = reqKey{dev, sub, addr}
	}
	c.mu.Lock()
	ch, ok := c.pending[key]
	if !ok && dev == 0x00 {
		// Some directly connected devices answer with index 0 instead of 0xFF.
		key.dev = devReceiver
		ch, ok = c.pending[key]
	}
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if ok {
		cp := append([]byte(nil), pkt...)
		select {
		case ch <- cp:
		default:
		}
		return
	}
	if c.notify != nil {
		c.notify(append([]byte(nil), pkt...))
	}
}

func (c *Channel) Close() {
	c.mu.Lock()
	c.closed = true
	for k, ch := range c.pending {
		close(ch)
		delete(c.pending, k)
	}
	c.mu.Unlock()
	c.link.Close()
}

func (c *Channel) roundTrip(report []byte, key reqKey) ([]byte, error) {
	c.reqLock.Lock()
	defer c.reqLock.Unlock()
	ch := make(chan []byte, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.link.Send(report); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case pkt, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if pkt[2] == 0xFF {
			return nil, &HidppError{Code: pkt[5]}
		}
		if pkt[2] == 0x8F {
			return nil, &HidppError{V10: true, Code: pkt[5]}
		}
		return pkt, nil
	case <-time.After(c.Timeout):
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, ErrTimeout
	}
}

// Call performs a HID++ 2.0 feature function call and returns the params (16 bytes).
func (c *Channel) Call(dev, featIdx, fn byte, params ...byte) ([]byte, error) {
	rep := make([]byte, 20)
	rep[0] = reportLong
	rep[1] = dev
	rep[2] = featIdx
	rep[3] = fn<<4 | swID
	copy(rep[4:], params)
	pkt, err := c.roundTrip(rep, reqKey{dev, featIdx, fn<<4 | swID})
	if err != nil {
		return nil, err
	}
	return pkt[4:], nil
}

// Register access (HID++ 1.0) on the receiver. sub = 0x80 set / 0x81 get.
func (c *Channel) Register(dev, sub, reg byte, p0, p1, p2 byte) ([]byte, error) {
	if !c.link.HasShort() {
		return nil, errors.New("no short report channel")
	}
	rep := []byte{reportShort, dev, sub, reg, p0, p1, p2}
	pkt, err := c.roundTrip(rep, reqKey{dev, sub, reg})
	if err != nil {
		return nil, err
	}
	return pkt[4:], nil
}

// Ping returns the HID++ protocol version of a device.
func (c *Channel) Ping(dev byte) (major, minor byte, err error) {
	p, err := c.Call(dev, 0x00, 1, 0, 0, 0x5A)
	if err != nil {
		var he *HidppError
		if errors.As(err, &he) && he.V10 && he.Code == 0x01 {
			return 1, 0, nil // HID++ 1.0 device that is online
		}
		return 0, 0, err
	}
	return p[0], p[1], nil
}

// FeatureIndex asks the root feature for the index of a feature (0 = missing).
func (c *Channel) FeatureIndex(dev byte, feature uint16) (byte, error) {
	p, err := c.Call(dev, 0x00, 0, byte(feature>>8), byte(feature))
	if err != nil {
		return 0, err
	}
	return p[0], nil
}

func be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }
func sbe16(b []byte) int16 { return int16(be16(b)) }

// RegisterLong writes a long (HID++ 1.0) register on the receiver, e.g. Bolt pairing 0x2C1.
func (c *Channel) RegisterLong(dev, reg byte, params ...byte) ([]byte, error) {
	rep := make([]byte, 20)
	rep[0] = reportLong
	rep[1] = dev
	rep[2] = 0x82
	rep[3] = reg
	copy(rep[4:], params)
	pkt, err := c.roundTrip(rep, reqKey{dev, 0x82, reg})
	if err != nil {
		return nil, err
	}
	return pkt[4:], nil
}
