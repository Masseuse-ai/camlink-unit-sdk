package ble

import "testing"

func TestUUIDForms(t *testing.T) {
	cases := []struct {
		in        UUID
		canonical string
		short     string
	}{
		{"fff0", "0000fff0-0000-1000-8000-00805f9b34fb", "fff0"},
		{"FFF4", "0000fff4-0000-1000-8000-00805f9b34fb", "fff4"},
		{"0000FFF5-0000-1000-8000-00805F9B34FB", "0000fff5-0000-1000-8000-00805f9b34fb", "fff5"},
		{"0000fff0", "0000fff0-0000-1000-8000-00805f9b34fb", "fff0"},
		{"6e400001-b5a3-f393-e0a9-e50e24dcca9e", "6e400001-b5a3-f393-e0a9-e50e24dcca9e", "6e400001-b5a3-f393-e0a9-e50e24dcca9e"},
	}
	for _, c := range cases {
		if got := c.in.Canonical(); got != c.canonical {
			t.Errorf("%q.Canonical() = %q, want %q", c.in, got, c.canonical)
		}
		if got := c.in.Short(); got != c.short {
			t.Errorf("%q.Short() = %q, want %q", c.in, got, c.short)
		}
	}
	if !UUID("fff0").Equal("0000FFF0-0000-1000-8000-00805F9B34FB") {
		t.Error("short and long forms should be equal")
	}
	if UUID("fff0").Equal("fff1") {
		t.Error("different UUIDs should not be equal")
	}
}

func TestAdvertisementHasService(t *testing.T) {
	a := Advertisement{Services: []UUID{"0000fff0-0000-1000-8000-00805f9b34fb", "180a"}}
	if !a.HasService("FFF0") || !a.HasService("180A") {
		t.Error("expected both services to match in either form")
	}
	if a.HasService("fff1") {
		t.Error("unexpected match")
	}
}

func TestManufacturerData(t *testing.T) {
	a := Advertisement{ManufacturerData: map[uint16][]byte{0x0059: {1, 2, 3}}}
	if d, ok := a.Manufacturer(0x0059); !ok || len(d) != 3 {
		t.Fatalf("Manufacturer(0x0059) = %v, %v", d, ok)
	}
	if _, ok := a.Manufacturer(0x004c); ok {
		t.Fatal("a company not advertised was found")
	}
	if _, ok := (Advertisement{}).Manufacturer(0x0059); ok {
		t.Fatal("an advertisement without manufacturer data answered")
	}
}
