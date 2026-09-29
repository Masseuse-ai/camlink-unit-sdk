//go:build windows

package ble

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unsafe"

	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
)

// The Windows advertiser is a BluetoothLEAdvertisementPublisher: its
// advertisement takes manufacturer data sections (a local name is not
// accepted in a publisher's advertisement, so Broadcast.Name is dropped
// here). A change of broadcast is a stop and a start with a new publisher.

type advertiser struct {
	log *slog.Logger

	mu        sync.Mutex
	publisher *advertisement.BluetoothLEAdvertisementPublisher
	closed    bool
}

// OpenAdvertiser checks the runtime and the adapter as Open does, then
// returns an Advertiser with nothing on the air.
func OpenAdvertiser(ctx context.Context, log *slog.Logger) (a Advertiser, err error) {
	defer func() {
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("%w: the Windows Runtime failed: %v", ErrUnavailable, r)
		}
	}()
	c, err := open(ctx, log)
	if err != nil {
		return nil, err
	}
	cen := c.(*central)
	if !adapterSupportsPeripheral(cen.adapter) {
		_ = cen.Close()
		return nil, fmt.Errorf("%w: this computer's Bluetooth does not transmit advertisements", ErrUnsupported)
	}
	_ = cen.Close()
	return &advertiser{log: cen.log}, nil
}

// Advertise builds a publisher for b, starts it and waits for the system
// to report it started.
func (a *advertiser) Advertise(ctx context.Context, b Broadcast) (err error) {
	if len(b.ManufacturerData) == 0 && len(b.Services) == 0 {
		return ErrNoBroadcast
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: the Windows Runtime failed: %v", ErrUnavailable, r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("ble: advertiser is closed")
	}
	a.stopLocked()

	pub, err := advertisement.NewBluetoothLEAdvertisementPublisher()
	if err != nil {
		return fmt.Errorf("ble: creating the publisher: %w", err)
	}
	adv, err := pub.GetAdvertisement()
	if err != nil {
		pub.Release()
		return fmt.Errorf("ble: the publisher's advertisement: %w", err)
	}
	defer adv.Release()
	if len(b.ManufacturerData) > 0 {
		list, err := adv.GetManufacturerData()
		if err != nil {
			pub.Release()
			return fmt.Errorf("ble: the advertisement's manufacturer data: %w", err)
		}
		defer list.Release()
		for company, data := range b.ManufacturerData {
			buf, err := bytesBuffer(data)
			if err != nil {
				pub.Release()
				return fmt.Errorf("ble: %w", err)
			}
			item, err := advertisement.BluetoothLEManufacturerDataCreate(company, buf)
			buf.Release()
			if err != nil {
				pub.Release()
				return fmt.Errorf("ble: manufacturer data: %w", err)
			}
			err = list.Append(unsafe.Pointer(item))
			item.Release()
			if err != nil {
				pub.Release()
				return fmt.Errorf("ble: manufacturer data: %w", err)
			}
		}
	}
	if len(b.Services) > 0 {
		list, err := adv.GetServiceUuids()
		if err != nil {
			pub.Release()
			return fmt.Errorf("ble: the advertisement's services: %w", err)
		}
		defer list.Release()
		for _, u := range b.Services {
			g := uuidToGUID(u)
			if err := list.Append(unsafe.Pointer(&g)); err != nil {
				pub.Release()
				return fmt.Errorf("ble: services: %w", err)
			}
		}
	}
	if err := pub.Start(); err != nil {
		pub.Release()
		return fmt.Errorf("ble: starting the publisher: %w", err)
	}
	a.publisher = pub
	// The publisher has no event this binding exposes; its status is
	// polled until it is on the air or gave up.
	for {
		st, err := pub.GetStatus()
		if err != nil {
			return fmt.Errorf("ble: the publisher's status: %w", err)
		}
		switch st {
		case advertisement.BluetoothLEAdvertisementPublisherStatusStarted:
			return nil
		case advertisement.BluetoothLEAdvertisementPublisherStatusAborted:
			a.stopLocked()
			return fmt.Errorf("%w: the system refused the advertisement", ErrUnavailable)
		}
		select {
		case <-ctx.Done():
			a.stopLocked()
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Stop takes the advertisement off the air.
func (a *advertiser) Stop(context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: the Windows Runtime failed: %v", ErrUnavailable, r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked()
	return nil
}

func (a *advertiser) stopLocked() {
	if a.publisher == nil {
		return
	}
	_ = a.publisher.Stop()
	a.publisher.Release()
	a.publisher = nil
}

// Close stops and forgets the advertiser.
func (a *advertiser) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.stopLocked()
	return nil
}
