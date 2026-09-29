package helper_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Masseuse-ai/camlink-unit-sdk/helper"
	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

// fakeDriver is a two-motor device with a battery: intensity channel A for
// the connector of today, two actuators and a sensor for the model.
type fakeDriver struct {
	mu       sync.Mutex
	levels   map[string]int
	released int
	closed   bool
}

func newFake() *fakeDriver { return &fakeDriver{levels: map[string]int{}} }

func (d *fakeDriver) Kind() unit.Kind { return "fake" }
func (d *fakeDriver) Label() string   { return "Fake Two (F1)" }
func (d *fakeDriver) Port() string    { return "fake-port" }
func (d *fakeDriver) Identity() unit.Identity {
	return unit.Identity{Maker: "Fake", Model: "Two", Tag: "F1"}
}
func (d *fakeDriver) Capabilities() unit.Capabilities {
	return unit.Capabilities{LevelMax: 20, Channels: []string{"a"}, Modes: []int{1}}
}
func (d *fakeDriver) Release(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.released++
	for k := range d.levels {
		d.levels[k] = 0
	}
	return nil
}
func (d *fakeDriver) Arm(context.Context, string) error { return nil }
func (d *fakeDriver) Status(context.Context) (unit.Status, error) {
	return unit.Status{Connected: true, LevelA: unit.Int(d.level("motor1"))}, nil
}
func (d *fakeDriver) Telemetry(context.Context) (unit.Frame, error) {
	return unit.Frame{LevelA: unit.Int(d.level("motor1"))}, nil
}
func (d *fakeDriver) Execute(_ context.Context, cmd unit.Command, levelMax int, _ func() bool) (unit.Result, error) {
	if cmd.Verb == "set_level" && cmd.Level != nil {
		d.set("motor1", min(*cmd.Level, levelMax))
	}
	return unit.Result{Verb: cmd.Verb, Level: unit.Int(d.level("motor1"))}, nil
}
func (d *fakeDriver) Close(context.Context, bool) error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return nil
}

func (d *fakeDriver) Actuators() []unit.Actuator {
	return []unit.Actuator{
		{ID: "motor1", Kind: unit.ActuatorVibrate, Label: "Motor 1", Max: 20},
		{ID: "motor2", Kind: unit.ActuatorVibrate, Label: "Motor 2", Max: 20},
	}
}
func (d *fakeDriver) Actuate(_ context.Context, cmd unit.Actuate) error {
	switch {
	case cmd.Stop:
		d.set(cmd.Actuator, 0)
	case cmd.Level != nil:
		d.set(cmd.Actuator, *cmd.Level)
	}
	return nil
}
func (d *fakeDriver) Sensors() []unit.Sensor {
	return []unit.Sensor{{ID: "battery", Kind: unit.SensorBattery, Min: 0, Max: 100}}
}
func (d *fakeDriver) Readings(context.Context) ([]unit.Reading, error) {
	return []unit.Reading{{Sensor: "battery", Value: 77, AtMs: 1}}, nil
}
func (d *fakeDriver) set(id string, v int) { d.mu.Lock(); d.levels[id] = v; d.mu.Unlock() }
func (d *fakeDriver) level(id string) int  { d.mu.Lock(); defer d.mu.Unlock(); return d.levels[id] }

type fakeFinder struct{ drv *fakeDriver }

func (f fakeFinder) Find(context.Context) (unit.Driver, error) { return f.drv, nil }
func (f fakeFinder) Describe(_ context.Context, out io.Writer) error {
	fmt.Fprintln(out, "one fake unit")
	return nil
}
func (f fakeFinder) List(context.Context) ([]unit.Unit, error) {
	return []unit.Unit{{ID: "fake-port", Kind: "fake", Label: "Fake Two (F1)"}}, nil
}

// exchange runs Serve over pipes and returns a function that sends one
// request and returns its answer.
func exchange(t *testing.T, finder unit.Finder) (call func(method string, params any) helper.Envelope, stop func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- helper.Serve(context.Background(), inR, outW, finder, helper.Options{Name: "fake", Kinds: []unit.Kind{"fake"}})
	}()
	reader := bufio.NewReader(outR)
	var id uint64
	call = func(method string, params any) helper.Envelope {
		id++
		n := id
		env := helper.Envelope{ID: &n, Method: method, Params: helper.MustJSON(params)}
		line, _ := json.Marshal(env)
		if _, err := inW.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		reply, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var out helper.Envelope
		if err := json.Unmarshal([]byte(reply), &out); err != nil {
			t.Fatal(err)
		}
		if out.ID == nil || *out.ID != n {
			t.Fatalf("answer to %d came with id %v", n, out.ID)
		}
		return out
	}
	stop = func() {
		_ = inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return when stdin ended")
		}
	}
	return call, stop
}

func decode[T any](t *testing.T, env helper.Envelope) T {
	t.Helper()
	if env.Error != nil {
		t.Fatalf("error answer: %+v", env.Error)
	}
	var v T
	if err := json.Unmarshal(env.Result, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGuestServesTheActuatorModel(t *testing.T) {
	drv := newFake()
	call, stop := exchange(t, fakeFinder{drv})

	hello := decode[helper.Hello](t, call(helper.MethodHello, nil))
	if hello.Protocol != helper.Protocol || hello.Name != "fake" || len(hello.Kinds) != 1 {
		t.Fatalf("hello: %+v", hello)
	}
	desc := decode[helper.DescribeResult](t, call(helper.MethodDescribe, nil))
	if !strings.Contains(desc.Text, "one fake unit") {
		t.Fatalf("describe: %q", desc.Text)
	}
	list := decode[helper.ListResult](t, call(helper.MethodList, nil))
	if len(list.Units) != 1 || list.Units[0].ID != "fake-port" {
		t.Fatalf("list: %+v", list)
	}
	if env := call(helper.MethodSelect, helper.SelectParams{Unit: "x"}); env.Error == nil || env.Error.Code != helper.CodeUnsupported {
		t.Fatalf("select on a finder that cannot: %+v", env.Error)
	}
	if env := call(helper.MethodActuate, helper.ActuateParams{Command: unit.Actuate{Actuator: "motor1", Level: unit.Int(3)}}); env.Error == nil || env.Error.Code != helper.CodeNoDriver {
		t.Fatalf("actuate before find: %+v", env.Error)
	}

	found := decode[helper.Found](t, call(helper.MethodFind, nil))
	if found.Kind != "fake" || found.Maker != "Fake" || found.Tag != "F1" || found.Capabilities.LevelMax != 20 {
		t.Fatalf("found: %+v", found)
	}
	if len(found.Actuators) != 2 || found.Actuators[1].ID != "motor2" || len(found.Sensors) != 1 || found.Sensors[0].Kind != unit.SensorBattery {
		t.Fatalf("found's model: %+v %+v", found.Actuators, found.Sensors)
	}

	// The intensity path of today.
	res := decode[helper.ExecuteResult](t, call(helper.MethodExecute, helper.ExecuteParams{Command: unit.Command{Verb: "set_level", Level: unit.Int(12)}, LevelMax: 10}))
	if res.Result.Level == nil || *res.Result.Level != 10 || drv.level("motor1") != 10 {
		t.Fatalf("execute: %+v, motor1 %d", res.Result, drv.level("motor1"))
	}

	// The actuator path: checked against the driver's actuators.
	decode[struct{}](t, call(helper.MethodActuate, helper.ActuateParams{Command: unit.Actuate{Actuator: "motor2", Level: unit.Int(7)}}))
	if drv.level("motor2") != 7 {
		t.Fatalf("motor2 %d", drv.level("motor2"))
	}
	if env := call(helper.MethodActuate, helper.ActuateParams{Command: unit.Actuate{Actuator: "motor2", Level: unit.Int(21)}}); env.Error == nil || env.Error.Code != helper.CodeError {
		t.Fatalf("a level past Max should be refused: %+v", env.Error)
	}
	if env := call(helper.MethodActuate, helper.ActuateParams{Command: unit.Actuate{Actuator: "motor9", Stop: true}}); env.Error == nil || env.Error.Code != helper.CodeNoActuator {
		t.Fatalf("an unknown actuator should be no_actuator: %+v", env.Error)
	}
	readings := decode[helper.ReadingsResult](t, call(helper.MethodReadings, nil))
	if len(readings.Readings) != 1 || readings.Readings[0].Value != 77 {
		t.Fatalf("readings: %+v", readings)
	}

	decode[struct{}](t, call(helper.MethodRelease, nil))
	if drv.level("motor1") != 0 || drv.level("motor2") != 0 || drv.released != 1 {
		t.Fatalf("release left %d/%d, released %d", drv.level("motor1"), drv.level("motor2"), drv.released)
	}
	decode[struct{}](t, call(helper.MethodClose, helper.CloseParams{Restore: true}))
	stop()
	if !drv.closed {
		t.Fatal("close did not reach the driver")
	}
}

// A driver without the model answers actuate and readings as unsupported,
// and a Host of protocol 1 sees a `find` it can read.
func TestGuestWithoutActuators(t *testing.T) {
	type plain struct{ unit.Driver }
	drv := plain{newFake()}
	call, stop := exchange(t, stubFinder{drv})
	defer stop()
	found := decode[helper.Found](t, call(helper.MethodFind, nil))
	if found.Actuators != nil || found.Sensors != nil {
		t.Fatalf("a plain driver reports no model: %+v", found)
	}
	if env := call(helper.MethodActuate, helper.ActuateParams{Command: unit.Actuate{Actuator: "motor1", Stop: true}}); env.Error == nil || env.Error.Code != helper.CodeUnsupported {
		t.Fatalf("actuate on a plain driver: %+v", env.Error)
	}
	if env := call(helper.MethodReadings, nil); env.Error == nil || env.Error.Code != helper.CodeUnsupported {
		t.Fatalf("readings on a plain driver: %+v", env.Error)
	}
}

type stubFinder struct{ drv unit.Driver }

func (f stubFinder) Find(context.Context) (unit.Driver, error) { return f.drv, nil }
func (f stubFinder) Describe(context.Context, io.Writer) error { return nil }

func TestWireErrorsRoundTrip(t *testing.T) {
	cases := []struct {
		err    error
		target error
		reason string
	}{
		{unit.ErrNoDevice, unit.ErrNoDevice, ""},
		{fmt.Errorf("%w: nothing in reach", unit.ErrNoDevice), unit.ErrNoDevice, ""},
		{unit.ErrCancelled, unit.ErrCancelled, ""},
		{unit.ErrArmed, unit.ErrArmed, ""},
		{fmt.Errorf("%w: %q", unit.ErrNoActuator, "x"), unit.ErrNoActuator, ""},
		{&unit.LossError{Reason: unit.ReasonLinkLost, Err: fmt.Errorf("gone")}, nil, unit.ReasonLinkLost},
	}
	for _, c := range cases {
		back := helper.FromWire(helper.ToWire(c.err))
		if c.target != nil && !errors.Is(back, c.target) {
			t.Errorf("%v came back as %v", c.err, back)
		}
		if c.reason != "" {
			var loss *unit.LossError
			if !errors.As(back, &loss) || loss.Reason != c.reason {
				t.Errorf("%v came back as %v", c.err, back)
			}
		}
	}
	if helper.FromWire(nil) != nil {
		t.Fatal("nil should stay nil")
	}
	if e := helper.ToWire(fmt.Errorf("plain")); e.Code != helper.CodeError || e.Message != "plain" {
		t.Fatalf("plain error: %+v", e)
	}
}
