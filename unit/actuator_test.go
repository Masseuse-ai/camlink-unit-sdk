package unit_test

import (
	"errors"
	"testing"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

var toy = []unit.Actuator{
	{ID: "motor1", Kind: unit.ActuatorVibrate, Max: 20, Patterns: []int{1, 2, 3}},
	{ID: "rotation", Kind: unit.ActuatorRotate, Max: 20, Signed: true},
	{ID: "arm", Kind: unit.ActuatorPosition, Max: 100, Timed: true},
	{ID: "lamp", Kind: unit.ActuatorLight, Max: 255, Colored: true},
	{ID: "zap", Kind: unit.ActuatorShock, Max: 100, Timed: true},
}

func i(v int) *int       { return &v }
func s(v string) *string { return &v }

func TestCheckActuateAccepts(t *testing.T) {
	ok := []unit.Actuate{
		{Actuator: "motor1", Level: i(0)},
		{Actuator: "motor1", Level: i(20)},
		{Actuator: "motor1", Pattern: i(2)},
		{Actuator: "motor1", Stop: true},
		{Actuator: "rotation", Level: i(-20)},
		{Actuator: "arm", Position: i(100), DurationMs: i(1500)},
		{Actuator: "arm", Position: i(0)},
		{Actuator: "lamp", Level: i(128), Color: s("#FF8800")},
		{Actuator: "zap", Level: i(30), DurationMs: i(500)},
	}
	for _, c := range ok {
		if err := unit.CheckActuate(c, toy, 100); err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}
}

func TestCheckActuateRefuses(t *testing.T) {
	bad := []unit.Actuate{
		{Actuator: "nope", Level: i(1)},
		{Actuator: "motor1"},
		{Actuator: "motor1", Level: i(1), Stop: true},
		{Actuator: "motor1", Level: i(21)},
		{Actuator: "motor1", Level: i(-1)},
		{Actuator: "motor1", Pattern: i(9)},
		{Actuator: "motor1", Level: i(5), DurationMs: i(10)},
		{Actuator: "motor1", Level: i(5), Color: s("#000000")},
		{Actuator: "motor1", Position: i(5)},
		{Actuator: "rotation", Level: i(-21)},
		{Actuator: "arm", Position: i(101)},
		{Actuator: "arm", Position: i(10), DurationMs: i(unit.DurationMsCap + 1)},
		{Actuator: "lamp", Level: i(1), Color: s("red")},
	}
	for _, c := range bad {
		if err := unit.CheckActuate(c, toy, 100); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if err := unit.CheckActuate(unit.Actuate{Actuator: "nope", Stop: true}, toy, 100); !errors.Is(err, unit.ErrNoActuator) {
		t.Fatalf("unknown actuator: %v", err)
	}
}

func TestCheckActuateCap(t *testing.T) {
	// A 50 percent cap halves every ceiling, position and rotation both.
	if err := unit.CheckActuate(unit.Actuate{Actuator: "motor1", Level: i(10)}, toy, 50); err != nil {
		t.Fatal(err)
	}
	if err := unit.CheckActuate(unit.Actuate{Actuator: "motor1", Level: i(11)}, toy, 50); err == nil {
		t.Fatal("11 of 20 passed a 50 percent cap")
	}
	if err := unit.CheckActuate(unit.Actuate{Actuator: "rotation", Level: i(-11)}, toy, 50); err == nil {
		t.Fatal("-11 of 20 passed a 50 percent cap")
	}
	if err := unit.CheckActuate(unit.Actuate{Actuator: "arm", Position: i(51)}, toy, 50); err == nil {
		t.Fatal("position 51 passed a 50 percent cap")
	}
	// An out-of-range cap means none.
	if err := unit.CheckActuate(unit.Actuate{Actuator: "motor1", Level: i(20)}, toy, 0); err != nil {
		t.Fatal(err)
	}
}

func TestStopAll(t *testing.T) {
	stops := unit.StopAll(toy)
	if len(stops) != len(toy) {
		t.Fatalf("%d stops for %d actuators", len(stops), len(toy))
	}
	for k, c := range stops {
		if c.Actuator != toy[k].ID || !c.Stop {
			t.Fatalf("stop %d: %+v", k, c)
		}
		if err := unit.CheckActuate(c, toy, 100); err != nil {
			t.Fatal(err)
		}
	}
}
