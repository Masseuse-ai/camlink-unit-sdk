// Package bletest is an in-memory peripheral and central for a driver's
// tests: the peripheral has the characteristics its real counterpart
// presents, records every write, answers reads from a table or a
// function, and pushes notifications on demand; the central sees the
// peripherals it was given and connects to them. A family's fake unit is
// a Peripheral with the family's characteristics and an OnWrite that
// keeps the unit's state.
package bletest

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Masseuse-ai/camlink-unit-sdk/ble"
)

// A Write is one recorded write.
type Write struct {
	Service, Char ble.UUID
	Data          []byte
	WithResponse  bool
}

// A Peripheral is one fake unit.
type Peripheral struct {
	ID   string
	Name string
	// Advertised are the service UUIDs in the advertisement; RSSI its
	// strength; Manufacturer its manufacturer data, if any.
	Advertised   []ble.UUID
	RSSI         int
	Manufacturer map[uint16][]byte
	// Chars are what Connect discovers; a write, read or subscribe to a
	// characteristic not here fails with ble.ErrNoCharacteristic, and one
	// without the property fails the same way.
	Chars []ble.Characteristic
	// Reads answers a read of a characteristic (by canonical UUID) with a
	// fixed value; OnRead, when set, is asked first and may decline.
	Reads  map[string][]byte
	OnRead func(char ble.UUID) ([]byte, bool)
	// OnWrite sees every write after it is recorded and may answer with a
	// notification on a characteristic (nil for none), as a unit that
	// acknowledges commands does.
	OnWrite func(char ble.UUID, data []byte) (notifyChar ble.UUID, notify []byte)
	// MTUValue is what MTU reports; ble.DefaultMTU when zero.
	MTUValue int

	mu     sync.Mutex
	writes []Write
	subs   map[string]func([]byte)
	closed bool
	gone   chan struct{}
	conns  int
}

// New is a peripheral with the id and name, advertising the services.
func New(id, name string, advertised ...ble.UUID) *Peripheral {
	return &Peripheral{ID: id, Name: name, Advertised: advertised, RSSI: -50, Reads: map[string][]byte{}, subs: map[string]func([]byte){}, gone: make(chan struct{})}
}

// Add declares a characteristic under a service with the properties.
func (p *Peripheral) Add(service, char ble.UUID, props ble.Properties) *Peripheral {
	p.Chars = append(p.Chars, ble.Characteristic{UUID: ble.UUID(char.Canonical()), Service: ble.UUID(service.Canonical()), Properties: props})
	return p
}

// Advertisement is what a scan sees.
func (p *Peripheral) Advertisement() ble.Advertisement {
	return ble.Advertisement{ID: p.ID, Name: p.Name, Services: append([]ble.UUID(nil), p.Advertised...), ManufacturerData: p.Manufacturer, RSSI: p.RSSI}
}

// Writes are the writes so far, in order.
func (p *Peripheral) Writes() []Write {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Write(nil), p.writes...)
}

// WritesTo are the data written to one characteristic, in order.
func (p *Peripheral) WritesTo(char ble.UUID) [][]byte {
	var out [][]byte
	for _, w := range p.Writes() {
		if w.Char.Equal(char) {
			out = append(out, w.Data)
		}
	}
	return out
}

// LastWrite is the last write to the characteristic, nil for none.
func (p *Peripheral) LastWrite(char ble.UUID) []byte {
	ws := p.WritesTo(char)
	if len(ws) == 0 {
		return nil
	}
	return ws[len(ws)-1]
}

// Notify pushes a value on a characteristic to its subscriber, if any.
func (p *Peripheral) Notify(char ble.UUID, data []byte) {
	p.mu.Lock()
	fn := p.subs[char.Canonical()]
	p.mu.Unlock()
	if fn != nil {
		fn(append([]byte(nil), data...))
	}
}

// Subscribed says whether the characteristic has a subscriber.
func (p *Peripheral) Subscribed(char ble.UUID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subs[char.Canonical()] != nil
}

// Connections is how many times the peripheral was connected to.
func (p *Peripheral) Connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conns
}

// Drop ends the link from the peripheral's side.
func (p *Peripheral) Drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.gone)
	}
}

// Dropped says whether the link ended.
func (p *Peripheral) Dropped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *Peripheral) char(char ble.UUID) (ble.Characteristic, bool) {
	for _, c := range p.Chars {
		if c.UUID.Equal(char) {
			return c, true
		}
	}
	return ble.Characteristic{}, false
}

// -- ble.Conn -----------------------------------------------------------------

type conn struct{ p *Peripheral }

func (c conn) ID() string   { return c.p.ID }
func (c conn) Name() string { return c.p.Name }
func (c conn) MTU() int {
	if c.p.MTUValue > 0 {
		return c.p.MTUValue
	}
	return ble.DefaultMTU
}

func (c conn) Characteristics(service ble.UUID) []ble.Characteristic {
	var out []ble.Characteristic
	for _, ch := range c.p.Chars {
		if service == "" || ch.Service.Equal(service) {
			out = append(out, ch)
		}
	}
	return out
}

func (c conn) Write(ctx context.Context, service, char ble.UUID, data []byte, withResponse bool) error {
	p := c.p
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ble.ErrDisconnected
	}
	ch, ok := p.char(char)
	if !ok || !ch.Properties.Writable() {
		p.mu.Unlock()
		return ble.ErrNoCharacteristic
	}
	p.writes = append(p.writes, Write{Service: service, Char: ble.UUID(ch.UUID.Canonical()), Data: append([]byte(nil), data...), WithResponse: withResponse})
	onWrite := p.OnWrite
	p.mu.Unlock()
	if onWrite != nil {
		if nchar, ndata := onWrite(ch.UUID, append([]byte(nil), data...)); ndata != nil {
			go p.Notify(nchar, ndata)
		}
	}
	return nil
}

func (c conn) Subscribe(ctx context.Context, service, char ble.UUID, fn func([]byte)) error {
	p := c.p
	ch, ok := p.char(char)
	if !ok || !ch.Properties.Notifies() {
		return ble.ErrNoCharacteristic
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ble.ErrDisconnected
	}
	p.subs[ch.UUID.Canonical()] = fn
	return nil
}

func (c conn) Read(ctx context.Context, service, char ble.UUID) ([]byte, error) {
	p := c.p
	ch, ok := p.char(char)
	if !ok || !ch.Properties.Read {
		return nil, ble.ErrNoCharacteristic
	}
	p.mu.Lock()
	closed, onRead, fixed := p.closed, p.OnRead, p.Reads[ch.UUID.Canonical()]
	p.mu.Unlock()
	if closed {
		return nil, ble.ErrDisconnected
	}
	if onRead != nil {
		if v, ok := onRead(ch.UUID); ok {
			return append([]byte(nil), v...), nil
		}
	}
	if fixed == nil {
		return nil, ble.ErrNoCharacteristic
	}
	return append([]byte(nil), fixed...), nil
}

func (c conn) Disconnected() <-chan struct{} { return c.p.gone }
func (c conn) Close() error                  { c.p.Drop(); return nil }

// -- ble.Central --------------------------------------------------------------

// A Central sees the peripherals it was given.
type Central struct {
	Peripherals []*Peripheral
	// Held are peripherals another program holds (ConnectedWithService).
	Held []*Peripheral
}

// NewCentral is a central over the peripherals.
func NewCentral(ps ...*Peripheral) *Central { return &Central{Peripherals: ps} }

// Open is a finder's Open for tests: the central itself.
func (c *Central) Open(context.Context, *slog.Logger) (ble.Central, error) { return c, nil }

// Scan offers each peripheral's advertisement to match, in order, and
// waits for ctx when none is taken (a scan window running out).
func (c *Central) Scan(ctx context.Context, match func(ble.Advertisement) bool) (ble.Advertisement, error) {
	for _, p := range c.Peripherals {
		if a := p.Advertisement(); match(a) {
			return a, nil
		}
	}
	<-ctx.Done()
	return ble.Advertisement{}, ctx.Err()
}

func (c *Central) ConnectedWithService(_ context.Context, service ble.UUID) ([]ble.Advertisement, error) {
	var out []ble.Advertisement
	for _, p := range c.Held {
		a := p.Advertisement()
		a.Connected = true
		if a.HasService(service) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (c *Central) Connect(ctx context.Context, id string) (ble.Conn, error) {
	for _, p := range append(append([]*Peripheral(nil), c.Peripherals...), c.Held...) {
		if p.ID == id {
			p.mu.Lock()
			if p.closed {
				// A dropped peripheral comes back on the next connection.
				p.closed = false
				p.gone = make(chan struct{})
			}
			p.conns++
			p.mu.Unlock()
			return conn{p}, nil
		}
	}
	return nil, ble.ErrNotFound
}

func (c *Central) Close() error { return nil }
