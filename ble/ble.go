// Package ble is a minimal Bluetooth Low Energy central: enough to find a
// peripheral, connect to it, write to a characteristic and receive its
// notifications; and, for the device that never connects and only listens
// for manufacturer data in advertisements, an Advertiser. It is pure Go so
// a helper and the connector keep cross-compiling with CGO_ENABLED=0: on
// macOS it drives CoreBluetooth through the Objective-C runtime (purego),
// on Linux it speaks to BlueZ over D-Bus, and on Windows it drives the
// Windows Runtime's Windows.Devices.Bluetooth through its COM vtables
// (winrt-go).
//
// Everything above the Central, Conn and Advertiser interfaces is tested
// against fakes; the backends themselves are exercised against real
// devices by the connector's `estim probe` and each family's exercise
// program.
package ble

import (
	"context"
	"errors"
	"strings"
)

// A UUID names a service or characteristic. Comparisons go through Equal so
// the 16-bit short form ("fff0") and the full Bluetooth base form
// ("0000fff0-0000-1000-8000-00805f9b34fb") name the same thing.
type UUID string

const baseSuffix = "-0000-1000-8000-00805f9b34fb"

// Canonical is the lower-case 128-bit form.
func (u UUID) Canonical() string {
	s := strings.ToLower(strings.TrimSpace(string(u)))
	switch len(s) {
	case 4:
		return "0000" + s + baseSuffix
	case 8:
		return s + baseSuffix
	}
	return s
}

// Short is the 16-bit form when the UUID is on the Bluetooth base, the
// canonical form otherwise.
func (u UUID) Short() string {
	c := u.Canonical()
	if len(c) == 36 && strings.HasPrefix(c, "0000") && strings.HasSuffix(c, baseSuffix) {
		return c[4:8]
	}
	return c
}

// Equal says whether two UUIDs name the same thing in any form.
func (u UUID) Equal(o UUID) bool { return u.Canonical() == o.Canonical() }

// An Advertisement is what a scan learned about a peripheral, or what the
// system knows about one it already holds a connection to.
type Advertisement struct {
	// ID is the system's identifier for the peripheral: a CoreBluetooth
	// UUID on macOS (per computer), the device address on Linux and
	// Windows (AA:BB:CC:DD:EE:FF).
	ID string
	// Name is the advertised local name, or the system's cached name.
	Name string
	// Services are the advertised service UUIDs, when any.
	Services []UUID
	// ManufacturerData is the manufacturer-specific data advertised, by
	// company identifier (the Bluetooth SIG's assigned numbers), when any:
	// what tells one vendor's connectionless device from another's.
	ManufacturerData map[uint16][]byte
	// RSSI is the signal strength in dBm, 0 when unknown.
	RSSI int
	// Connected says the system already holds a connection to it (the
	// ConnectedWithService path); such a peripheral does not advertise.
	Connected bool
}

// HasService says whether the advertisement names the service.
func (a Advertisement) HasService(u UUID) bool {
	for _, s := range a.Services {
		if s.Equal(u) {
			return true
		}
	}
	return false
}

// Manufacturer is the manufacturer-specific data advertised under the
// company identifier, and whether there was any.
func (a Advertisement) Manufacturer(company uint16) ([]byte, bool) {
	d, ok := a.ManufacturerData[company]
	return d, ok
}

// A Central finds peripherals and connects to them.
type Central interface {
	// Scan reports the first advertisement match accepts, or ctx's error.
	Scan(ctx context.Context, match func(Advertisement) bool) (Advertisement, error)
	// ConnectedWithService lists the peripherals the system already holds
	// a connection to (another program's) that offer the service. Where
	// the system cannot tell, the list is empty.
	ConnectedWithService(ctx context.Context, service UUID) ([]Advertisement, error)
	// Connect opens a connection to the peripheral with ID and discovers
	// its services and characteristics.
	Connect(ctx context.Context, id string) (Conn, error)
	// Close stops any scan and forgets the central; connections opened
	// through it stay usable until closed themselves.
	Close() error
}

// A Conn is one connected peripheral.
type Conn interface {
	ID() string
	Name() string
	// Write sends data to the characteristic, waiting for the peripheral's
	// acknowledgment when withResponse is set.
	Write(ctx context.Context, service, char UUID, data []byte, withResponse bool) error
	// Subscribe turns the characteristic's notifications on and delivers
	// each value to fn, in order, from one goroutine.
	Subscribe(ctx context.Context, service, char UUID, fn func([]byte)) error
	// Read reads the characteristic's value.
	Read(ctx context.Context, service, char UUID) ([]byte, error)
	// MTU is the most bytes one Write carries to the peripheral over this
	// link, as the system reports it; 0 when the system does not say. A
	// driver that must split a long command splits it at this.
	MTU() int
	// Disconnected is closed when the link drops, for whatever reason.
	Disconnected() <-chan struct{}
	// Close disconnects.
	Close() error
}

// DefaultMTU is the payload one write carries when the link negotiated
// nothing larger: the 23-byte ATT default less its 3-byte header.
const DefaultMTU = 20

// A Broadcast is what an Advertiser puts on the air: the advertisement a
// connectionless device listens for. Such a device (a toy that never
// connects, only obeys the manufacturer data it hears) is driven by
// replacing the broadcast, so Advertise is called for every command.
type Broadcast struct {
	// Name is the local name to advertise, when the system carries one
	// beside manufacturer data (Linux does; Windows and macOS do not).
	Name string
	// Services are service UUIDs to advertise, when any.
	Services []UUID
	// ManufacturerData is the payload, by company identifier. One company
	// is the rule.
	ManufacturerData map[uint16][]byte
}

// An Advertiser transmits advertisements: the peripheral role, as far as a
// connectionless device needs it. One is opened with OpenAdvertiser; its
// systems are Linux (BlueZ's LEAdvertisingManager1) and Windows (the
// Windows Runtime's BluetoothLEAdvertisementPublisher). macOS reports
// ErrUnsupported: CoreBluetooth's peripheral role advertises a name and
// services and no manufacturer data, which is what such a device listens
// for.
type Advertiser interface {
	// Advertise puts b on the air, replacing what was there, and returns
	// once the system reports it is being transmitted (or ctx ends).
	Advertise(ctx context.Context, b Broadcast) error
	// Stop takes the advertisement off the air; the Advertiser stays
	// usable.
	Stop(ctx context.Context) error
	// Close stops and releases the Advertiser.
	Close() error
}

// ErrNoBroadcast says a Broadcast carried nothing the system can transmit.
var ErrNoBroadcast = errors.New("ble: the broadcast has no manufacturer data, name or service")

// ErrUnsupported says this system has no Bluetooth backend.
var ErrUnsupported = errors.New("ble: Bluetooth is not supported on this system")

// ErrUnavailable says the system has Bluetooth but it cannot be used now:
// switched off, or this program was not allowed to use it.
var ErrUnavailable = errors.New("ble: Bluetooth is not available")

// ErrDisconnected says the peripheral dropped the link.
var ErrDisconnected = errors.New("ble: disconnected")

// ErrNoCharacteristic says the peripheral has no such characteristic.
var ErrNoCharacteristic = errors.New("ble: no such characteristic")

// ErrNotFound says no peripheral with that ID is known to the system.
var ErrNotFound = errors.New("ble: peripheral not found")
