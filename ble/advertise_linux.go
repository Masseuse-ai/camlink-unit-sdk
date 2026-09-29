//go:build linux

package ble

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// The Linux advertiser registers an org.bluez.LEAdvertisement1 object of
// its own on the system bus and hands it to the adapter's
// org.bluez.LEAdvertisingManager1. bluetoothd reads the object's properties
// (Type, LocalName, ServiceUUIDs, ManufacturerData) and puts them on the
// air; a change of broadcast is an unregister and a register, since
// bluetoothd reads the properties once.

const (
	ifaceAdvertising   = "org.bluez.LEAdvertisingManager1"
	ifaceAdvertisement = "org.bluez.LEAdvertisement1"
)

type advertiser struct {
	log     *slog.Logger
	bus     *dbus.Conn
	adapter dbus.ObjectPath
	path    dbus.ObjectPath

	mu         sync.Mutex
	registered bool
	props      *prop.Properties
	closed     bool
}

// OpenAdvertiser connects to the system bus and picks the first powered
// adapter, as Open does.
func OpenAdvertiser(ctx context.Context, log *slog.Logger) (Advertiser, error) {
	c, err := Open(ctx, log)
	if err != nil {
		return nil, err
	}
	cen := c.(*central)
	a := &advertiser{
		log:     cen.log,
		bus:     cen.bus,
		adapter: cen.adapter,
		path:    dbus.ObjectPath(fmt.Sprintf("/ai/masseuse/camlink/advertisement%d", os.Getpid())),
	}
	return a, nil
}

// Advertise registers b as this program's advertisement, first taking the
// previous one down.
func (a *advertiser) Advertise(ctx context.Context, b Broadcast) error {
	if len(b.ManufacturerData) == 0 && b.Name == "" && len(b.Services) == 0 {
		return ErrNoBroadcast
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("ble: advertiser is closed")
	}
	if err := a.unregisterLocked(ctx); err != nil {
		return err
	}
	// The advertisement object: read-only properties bluetoothd reads at
	// registration, and a Release method it calls when it drops us.
	md := make(map[uint16]dbus.Variant, len(b.ManufacturerData))
	for company, data := range b.ManufacturerData {
		md[company] = dbus.MakeVariant(append([]byte(nil), data...))
	}
	services := make([]string, 0, len(b.Services))
	for _, u := range b.Services {
		services = append(services, u.Canonical())
	}
	props := map[string]map[string]*prop.Prop{
		ifaceAdvertisement: {
			"Type":             {Value: "peripheral", Writable: false, Emit: prop.EmitFalse},
			"ManufacturerData": {Value: md, Writable: false, Emit: prop.EmitFalse},
			"ServiceUUIDs":     {Value: services, Writable: false, Emit: prop.EmitFalse},
			"LocalName":        {Value: b.Name, Writable: false, Emit: prop.EmitFalse},
			"Includes":         {Value: []string{}, Writable: false, Emit: prop.EmitFalse},
		},
	}
	if b.Name == "" {
		delete(props[ifaceAdvertisement], "LocalName")
	}
	p, err := prop.Export(a.bus, a.path, props)
	if err != nil {
		return fmt.Errorf("ble: exporting the advertisement: %w", err)
	}
	if err := a.bus.Export(advertisementObject{a}, a.path, ifaceAdvertisement); err != nil {
		return fmt.Errorf("ble: exporting the advertisement: %w", err)
	}
	node := &introspect.Node{Name: string(a.path), Interfaces: []introspect.Interface{
		introspect.IntrospectData, prop.IntrospectData,
		{Name: ifaceAdvertisement, Methods: []introspect.Method{{Name: "Release"}}, Properties: p.Introspection(ifaceAdvertisement)},
	}}
	if err := a.bus.Export(introspect.NewIntrospectable(node), a.path, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("ble: exporting the advertisement: %w", err)
	}
	a.props = p
	call := a.bus.Object(bluez, a.adapter).CallWithContext(ctx, ifaceAdvertising+".RegisterAdvertisement", 0, a.path, map[string]dbus.Variant{})
	if call.Err != nil {
		_ = a.bus.Export(nil, a.path, ifaceAdvertisement)
		var derr dbus.Error
		if errors.As(call.Err, &derr) && strings.Contains(derr.Name, "UnknownMethod") {
			return fmt.Errorf("%w: bluetoothd has no advertising manager", ErrUnsupported)
		}
		return fmt.Errorf("ble: registering the advertisement: %w", call.Err)
	}
	a.registered = true
	return nil
}

// Stop takes the advertisement off the air.
func (a *advertiser) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.unregisterLocked(ctx)
}

func (a *advertiser) unregisterLocked(ctx context.Context) error {
	if !a.registered {
		return nil
	}
	a.registered = false
	call := a.bus.Object(bluez, a.adapter).CallWithContext(ctx, ifaceAdvertising+".UnregisterAdvertisement", 0, a.path)
	_ = a.bus.Export(nil, a.path, ifaceAdvertisement)
	_ = a.bus.Export(nil, a.path, "org.freedesktop.DBus.Introspectable")
	_ = a.bus.Export(nil, a.path, ifaceProperties)
	if call.Err != nil {
		var derr dbus.Error
		// Already gone (bluetoothd released it) is not a failure.
		if errors.As(call.Err, &derr) && strings.Contains(derr.Name, "DoesNotExist") {
			return nil
		}
		return fmt.Errorf("ble: unregistering the advertisement: %w", call.Err)
	}
	return nil
}

// Close stops and lets the bus go.
func (a *advertiser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	err := a.unregisterLocked(ctx)
	a.mu.Unlock()
	if cerr := a.bus.Close(); err == nil {
		err = cerr
	}
	return err
}

// advertisementObject answers bluetoothd's Release: it dropped the
// advertisement (the adapter went away, or another program took the slot).
type advertisementObject struct{ a *advertiser }

func (o advertisementObject) Release() *dbus.Error {
	o.a.mu.Lock()
	o.a.registered = false
	o.a.mu.Unlock()
	o.a.log.Debug("ble: bluetoothd released the advertisement")
	return nil
}
