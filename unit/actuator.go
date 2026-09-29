package unit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// The actuator model.
//
// Capabilities, Command and Status describe a device as the connector has
// always driven one: intensity channels on a program, with a tempo. Most
// units are exactly that. A device that is more (a second motor, a
// rotation, a heater, a light, a piston that takes a position) declares
// its outputs as Actuators and its inputs as Sensors, and takes Actuate
// commands against them. A driver may be both: Capabilities for the
// intensity the connector drives today, Actuators for everything the
// device can do. Release stays the rule for both: every actuator to zero
// and stopped.

// An ActuatorKind says what an output does. The set is closed; a device
// with an output outside it is described by the nearest kind and its
// Label says the rest.
type ActuatorKind string

const (
	// ActuatorVibrate is a motor whose level is its strength.
	ActuatorVibrate ActuatorKind = "vibrate"
	// ActuatorRotate is a rotation; a Signed actuator reverses below zero.
	ActuatorRotate ActuatorKind = "rotate"
	// ActuatorOscillate is a to-and-fro motion at a level of speed.
	ActuatorOscillate ActuatorKind = "oscillate"
	// ActuatorReciprocate is a piston or arm moving in and out at a level
	// of speed; a device that takes positions instead is ActuatorPosition.
	ActuatorReciprocate ActuatorKind = "reciprocate"
	// ActuatorSuction is a pump drawing in at a level.
	ActuatorSuction ActuatorKind = "suction"
	// ActuatorInflate is a pump filling a bladder to a level.
	ActuatorInflate ActuatorKind = "inflate"
	// ActuatorConstrict tightens to a level.
	ActuatorConstrict ActuatorKind = "constrict"
	// ActuatorHeat warms to a level.
	ActuatorHeat ActuatorKind = "heat"
	// ActuatorLight is a lamp: a level of brightness and, when Colored, a
	// color.
	ActuatorLight ActuatorKind = "light"
	// ActuatorPosition places a linear actuator at a position on its
	// travel, over a duration when Timed: what a scripted stroke is made
	// of.
	ActuatorPosition ActuatorKind = "position"
	// ActuatorShock is one timed pulse at a level (a wearable's or a
	// collar's), never a sustained output.
	ActuatorShock ActuatorKind = "shock"
	// ActuatorEstim is an electrical stimulation channel at a level; on a
	// device the connector drives as intensity channels it is also there
	// as Capabilities.Channels.
	ActuatorEstim ActuatorKind = "estim"
	// ActuatorSwitch is on (Max) or off (0): a plug, a lock.
	ActuatorSwitch ActuatorKind = "switch"
	// ActuatorSound is a beeper or a speaker at a level.
	ActuatorSound ActuatorKind = "sound"
)

// An Actuator is one output of a device.
type Actuator struct {
	// ID names the actuator within its device, stable across connections
	// ("motor1", "rotation", "heater"): what an Actuate names.
	ID   string       `json:"id"`
	Kind ActuatorKind `json:"kind"`
	// Label is for people ("Motor 1", "Rotation"); empty means the kind.
	Label string `json:"label,omitempty"`
	// Max is the top of the actuator's own scale; zero is always off. A
	// Signed actuator also takes levels down to -Max.
	Max int `json:"max"`
	// Signed says negative levels are meaningful (a rotation's direction).
	Signed bool `json:"signed,omitempty"`
	// Timed says the actuator takes a duration: a position is reached over
	// it, a shock lasts it.
	Timed bool `json:"timed,omitempty"`
	// Colored says the actuator takes a color (a light).
	Colored bool `json:"colored,omitempty"`
	// Patterns are the device's own programs the actuator can run instead
	// of holding a level, by the device's numbers.
	Patterns []int `json:"patterns,omitempty"`
}

// A SensorKind says what an input measures.
type SensorKind string

const (
	// SensorBattery is the charge left, in percent.
	SensorBattery SensorKind = "battery"
	// SensorPressure is a squeeze or a grip, on the device's scale.
	SensorPressure SensorKind = "pressure"
	// SensorPosition is where a linear actuator is on its travel.
	SensorPosition SensorKind = "position"
	// SensorButton is a control on the device: pressed (1) or not (0).
	SensorButton SensorKind = "button"
	// SensorMotion is how much the device is moving, on its scale.
	SensorMotion SensorKind = "motion"
	// SensorTemperature is in tenths of a degree Celsius.
	SensorTemperature SensorKind = "temperature"
	// SensorDepth is how far the device is inserted, on its scale.
	SensorDepth SensorKind = "depth"
)

// A Sensor is one input of a device.
type Sensor struct {
	ID    string     `json:"id"`
	Kind  SensorKind `json:"kind"`
	Label string     `json:"label,omitempty"`
	// Min and Max bound Reading.Value.
	Min int `json:"min"`
	Max int `json:"max"`
}

// A Reading is one sensor's value at a time.
type Reading struct {
	Sensor string `json:"sensor"`
	Value  int    `json:"value"`
	AtMs   int64  `json:"atMs"`
}

// An Actuate is one instruction to one actuator. Numbers are pointers so
// an absent field is told from a zero. Exactly one of Level, Position,
// Pattern and Stop is given; Color goes with Level on a Colored actuator,
// DurationMs with Position or Level on a Timed one.
type Actuate struct {
	Actuator string `json:"actuator"`
	// Level is on the actuator's own scale, 0..Max, or -Max..Max when
	// Signed. Zero stops it.
	Level *int `json:"level,omitempty"`
	// Position is on the actuator's travel, 0..Max (ActuatorPosition).
	Position *int `json:"position,omitempty"`
	// DurationMs is how long the move or the pulse takes (Timed).
	DurationMs *int `json:"durationMs,omitempty"`
	// Color is "#rrggbb" (Colored).
	Color *string `json:"color,omitempty"`
	// Pattern is one of the actuator's Patterns.
	Pattern *int `json:"pattern,omitempty"`
	// Stop puts the actuator at zero and ends any pattern.
	Stop bool `json:"stop,omitempty"`
	// Reason is the caller's note for the log.
	Reason string `json:"reason,omitempty"`
}

// An Actuating driver's device has actuators. The connector, or a program
// using the driver directly, reads Actuators once after Find and sends
// Actuate commands checked with CheckActuate. Release (Driver) still puts
// every actuator at zero.
type Actuating interface {
	Actuators() []Actuator
	Actuate(ctx context.Context, cmd Actuate) error
}

// A Sensing driver's device has sensors it can read on demand; a device
// that also pushes readings delivers them through Telemetry.
type Sensing interface {
	Sensors() []Sensor
	Readings(ctx context.Context) ([]Reading, error)
}

// ActuatorsOf is d's actuators, nil for a driver that reports none.
func ActuatorsOf(d Driver) []Actuator {
	if a, ok := d.(Actuating); ok {
		return a.Actuators()
	}
	return nil
}

// SensorsOf is d's sensors, nil for a driver that reports none.
func SensorsOf(d Driver) []Sensor {
	if s, ok := d.(Sensing); ok {
		return s.Sensors()
	}
	return nil
}

// FindActuator is the actuator with the id among actuators, or false.
func FindActuator(actuators []Actuator, id string) (Actuator, bool) {
	for _, a := range actuators {
		if a.ID == id {
			return a, true
		}
	}
	return Actuator{}, false
}

// DurationMsCap bounds one timed move or pulse.
const DurationMsCap = 60_000

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ErrNoActuator is returned by CheckActuate for an id the device has not.
var ErrNoActuator = errors.New("unit: no such actuator")

// CheckActuate refuses a command the device cannot take or the caller may
// not send: an unknown actuator, a level or position off its scale, a
// duration or a color on an actuator that takes none, a pattern the
// actuator has not, two instructions at once, or a level past capPercent
// of the actuator's Max (100 for no cap), the ceiling the connector holds
// every actuator to under the attached session's settings.
func CheckActuate(cmd Actuate, actuators []Actuator, capPercent int) error {
	a, ok := FindActuator(actuators, cmd.Actuator)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoActuator, cmd.Actuator)
	}
	given := 0
	for _, g := range []bool{cmd.Level != nil, cmd.Position != nil, cmd.Pattern != nil, cmd.Stop} {
		if g {
			given++
		}
	}
	if given != 1 {
		return errors.New("unit: an actuate gives exactly one of level, position, pattern and stop")
	}
	if capPercent <= 0 || capPercent > 100 {
		capPercent = 100
	}
	ceiling := a.Max * capPercent / 100
	switch {
	case cmd.Level != nil:
		l := *cmd.Level
		if l < 0 && !a.Signed {
			return fmt.Errorf("unit: %s takes no negative level", a.ID)
		}
		if l > ceiling || l < -ceiling {
			return fmt.Errorf("unit: %s level must be within %d", a.ID, ceiling)
		}
	case cmd.Position != nil:
		if a.Kind != ActuatorPosition {
			return fmt.Errorf("unit: %s takes no position", a.ID)
		}
		if *cmd.Position < 0 || *cmd.Position > ceiling {
			return fmt.Errorf("unit: %s position must be 0..%d", a.ID, ceiling)
		}
	case cmd.Pattern != nil:
		found := false
		for _, p := range a.Patterns {
			if p == *cmd.Pattern {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unit: %s has no pattern %d", a.ID, *cmd.Pattern)
		}
	}
	if cmd.DurationMs != nil {
		if !a.Timed {
			return fmt.Errorf("unit: %s takes no duration", a.ID)
		}
		if *cmd.DurationMs < 0 || *cmd.DurationMs > DurationMsCap {
			return fmt.Errorf("unit: durationMs must be 0..%d", DurationMsCap)
		}
	}
	if cmd.Color != nil {
		if !a.Colored {
			return fmt.Errorf("unit: %s takes no color", a.ID)
		}
		if !colorRe.MatchString(*cmd.Color) {
			return errors.New("unit: color must be #rrggbb")
		}
	}
	return nil
}

// StopAll is the Actuate that stops every actuator, one command each, for
// a caller implementing its own release on an Actuating driver.
func StopAll(actuators []Actuator) []Actuate {
	out := make([]Actuate, 0, len(actuators))
	for _, a := range actuators {
		out = append(out, Actuate{Actuator: a.ID, Stop: true, Reason: "release"})
	}
	return out
}
