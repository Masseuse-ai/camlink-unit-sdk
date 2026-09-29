package bletest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Masseuse-ai/camlink-unit-sdk/ble"
)

const (
	svc = ble.UUID("0000fff0-0000-1000-8000-00805f9b34fb")
	tx  = ble.UUID("fff5")
	rx  = ble.UUID("fff4")
)

func newUnit() *Peripheral {
	p := New("dev-1", "Unit", svc)
	p.Add(svc, tx, ble.Properties{WriteWithoutResponse: true, Write: true})
	p.Add(svc, rx, ble.Properties{Notify: true, Read: true})
	p.Reads[rx.Canonical()] = []byte{7}
	return p
}

func TestRecordsWritesAndAnswersReads(t *testing.T) {
	p := newUnit()
	c := NewCentral(p)
	adv, err := c.Scan(context.Background(), func(a ble.Advertisement) bool { return a.HasService(svc) })
	if err != nil || adv.ID != "dev-1" {
		t.Fatalf("scan: %v %+v", err, adv)
	}
	conn, err := c.Connect(context.Background(), adv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(conn.Characteristics(svc)); got != 2 {
		t.Fatalf("characteristics: %d", got)
	}
	if err := conn.Write(context.Background(), svc, tx, []byte{1, 2}, false); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), svc, rx, []byte{1}, false); !errors.Is(err, ble.ErrNoCharacteristic) {
		t.Fatalf("write to a non-writable characteristic: %v", err)
	}
	if got := p.LastWrite(tx); !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("last write %x", got)
	}
	v, err := conn.Read(context.Background(), svc, rx)
	if err != nil || !bytes.Equal(v, []byte{7}) {
		t.Fatalf("read: %x %v", v, err)
	}
	if _, err := conn.Read(context.Background(), svc, tx); !errors.Is(err, ble.ErrNoCharacteristic) {
		t.Fatalf("read of a non-readable characteristic: %v", err)
	}
}

func TestNotifiesAndDrops(t *testing.T) {
	p := newUnit()
	p.OnWrite = func(char ble.UUID, data []byte) (ble.UUID, []byte) {
		if data[0] == 0x10 {
			return rx, []byte{0x90}
		}
		return "", nil
	}
	conn, _ := NewCentral(p).Connect(context.Background(), "dev-1")
	got := make(chan []byte, 2)
	if err := conn.Subscribe(context.Background(), svc, rx, func(b []byte) { got <- b }); err != nil {
		t.Fatal(err)
	}
	if !p.Subscribed(rx) {
		t.Fatal("not subscribed")
	}
	_ = conn.Write(context.Background(), svc, tx, []byte{0x10}, true)
	if b := <-got; !bytes.Equal(b, []byte{0x90}) {
		t.Fatalf("reply %x", b)
	}
	p.Notify(rx, []byte{1, 2, 3})
	if b := <-got; !bytes.Equal(b, []byte{1, 2, 3}) {
		t.Fatalf("notify %x", b)
	}
	p.Drop()
	select {
	case <-conn.Disconnected():
	default:
		t.Fatal("not disconnected")
	}
	if err := conn.Write(context.Background(), svc, tx, []byte{0}, false); !errors.Is(err, ble.ErrDisconnected) {
		t.Fatalf("write after drop: %v", err)
	}
	// Reconnecting brings the peripheral back.
	c2, err := NewCentral(p).Connect(context.Background(), "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Write(context.Background(), svc, tx, []byte{0}, false); err != nil {
		t.Fatalf("write after reconnect: %v", err)
	}
	if p.Connections() != 2 {
		t.Fatalf("connections %d", p.Connections())
	}
}

func TestHeldPeripherals(t *testing.T) {
	p := newUnit()
	c := &Central{Held: []*Peripheral{p}}
	held, err := c.ConnectedWithService(context.Background(), svc)
	if err != nil || len(held) != 1 || !held[0].Connected {
		t.Fatalf("held: %v %+v", err, held)
	}
	if _, err := c.Connect(context.Background(), "nope"); !errors.Is(err, ble.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
