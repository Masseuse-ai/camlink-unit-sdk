// Package helper is the guest side of a unit driver helper, and the wire
// both sides speak.
//
// The connector, github.com/Masseuse-ai/masseuse-camlink, carries one
// driver of its own. Drivers for other units are helper programs
// (`camlink-unit-<name>`, the connector's docs/UNITS.md) that it finds in
// its units directory and runs as child processes: the connector stays the
// program a person can read from end to end, and a helper is handed the
// unit's link (a serial port, a Bluetooth peripheral) and nothing else. It
// never sees the camera or the microphone, and it speaks to the service
// only through the connector.
//
// A helper program calls Main with a function that builds its family's
// unit.Finder; Serve then answers the connector's requests from that
// Finder and the unit.Driver it opens. The connector's own end, the Host,
// is in the connector's tree and uses this package's wire types.
//
// Wire: newline-delimited JSON on the helper's stdin and stdout. A request
// is {"id", "method", "params"}; its answer {"id", "result"} or {"id",
// "error": {"code", "message", "reason"}}. A message without an id is a
// notification: the Host's "cancel" ends a running execute early; the
// helper's stderr is its log, relayed line by line into the connector's.
// Requests may overlap (the connector lists units while a device is open,
// and samples telemetry between commands); each is answered on its own.
// The error codes carry the connector's own errors across: "no_device" is
// unit.ErrNoDevice, "loss" a unit.LossError with its reason (so the
// `device` report says why a unit went, helper or not), "cancelled"
// unit.ErrCancelled, "armed" unit.ErrArmed, "no_actuator"
// unit.ErrNoActuator.
package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

// Protocol is the version both ends speak; `hello` reports it. Version 2
// added the actuator model: `find` answers with the device's actuators
// and sensors, and `actuate` and `readings` drive and read them. A Host
// of version 1 ignores the additions; a helper of version 1 answers
// `actuate` with "unsupported".
const Protocol = 2

// Prefix is what a helper program's file name starts with in the units
// directory: `camlink-unit-<name>` (`.exe` on Windows).
const Prefix = "camlink-unit-"

// The probe hints a helper may declare (Hello.Probe, Options.Probe): how
// its finder identifies a unit on a port other families may share, so the
// connector probes the gentler families before the ones that write a
// command.
const (
	// ProbeListens: the finder listens for the unit's own signal and, on a
	// silent port it must speak to, sends only bytes another family
	// absorbs (the MK-312BT's sync zeros). Probed before ProbeWrites.
	ProbeListens = "listens"
	// ProbeWrites: the finder writes a command to identify its unit (the
	// E-Stim 2B's status read). The default, and what an older helper that
	// declares no hint is taken to be. Probed last.
	ProbeWrites = "writes"
)

// Methods.
const (
	MethodHello     = "hello"
	MethodDescribe  = "describe"
	MethodList      = "list"
	MethodSelect    = "select"
	MethodFind      = "find"
	MethodRelease   = "release"
	MethodArm       = "arm"
	MethodRenewArm  = "renewArm"
	MethodStatus    = "status"
	MethodTelemetry = "telemetry"
	MethodExecute   = "execute"
	MethodActuate   = "actuate"
	MethodReadings  = "readings"
	MethodClose     = "close"
	// MethodCancel is a notification from the Host: the execute with the
	// id given should stop as soon as it can (unit.Driver.Execute's
	// cancelled).
	MethodCancel = "cancel"
)

// Error codes.
const (
	CodeNoDevice    = "no_device"
	CodeLoss        = "loss"
	CodeCancelled   = "cancelled"
	CodeArmed       = "armed"
	CodeNoActuator  = "no_actuator"
	CodeNoDriver    = "no_driver"
	CodeUnsupported = "unsupported"
	CodeError       = "error"
)

// Envelope is one line either way. A request has Method (and ID unless it
// is a notification); an answer has ID and Result or Error.
type Envelope struct {
	ID     *uint64         `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WireError      `json:"error,omitempty"`
}

// WireError is an error as it crosses: a code both ends know, the message,
// and for a loss the reason.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

// Hello is what a helper says about itself.
type Hello struct {
	Protocol int `json:"protocol"`
	// Name is the family's name, what messages call it: the file name's
	// suffix, as a rule ("example" for camlink-unit-example).
	Name string `json:"name"`
	// Kinds the helper's driver may report.
	Kinds []unit.Kind `json:"kinds"`
	// Probe says how this family's finder identifies a unit on a port
	// other families may share, so the connector probes the gentler ones
	// first (ProbeListens before ProbeWrites). Empty is ProbeWrites: a
	// helper that predates the field, or one whose probe writes a command.
	Probe string `json:"probe,omitempty"`
}

// Found is `find`'s answer: the device opened, as the Host needs to present
// it before any other call.
type Found struct {
	Kind  unit.Kind `json:"kind"`
	Label string    `json:"label"`
	// Identity: the unit's name in three parts (`maker`, `model`, `tag`;
	// unit.IdentityReporter). A helper that predates them sends none and
	// the Host names the unit from Label.
	unit.Identity
	Port         string            `json:"port"`
	Capabilities unit.Capabilities `json:"capabilities"`
	// Held: another program on this computer has the unit open (unit.HeldReporter).
	Held bool `json:"held"`
	// RenewsArm: the driver has a countdown to renew (unit.ArmRenewer).
	RenewsArm bool `json:"renewsArm"`
	// Actuators and Sensors: the actuator model (unit.Actuating,
	// unit.Sensing); absent for a device that is intensity channels alone.
	Actuators []unit.Actuator `json:"actuators,omitempty"`
	Sensors   []unit.Sensor   `json:"sensors,omitempty"`
}

// The methods' parameters and results.
type (
	DescribeResult struct {
		Text string `json:"text"`
	}
	ListResult struct {
		Units []unit.Unit `json:"units"`
	}
	SelectParams struct {
		Unit string `json:"unit"`
	}
	ArmParams struct {
		PowerMode string `json:"powerMode"`
	}
	RenewArmParams struct {
		Until string `json:"until"`
	} // RFC 3339 with nanoseconds
	StatusResult struct {
		Status unit.Status `json:"status"`
	}
	TelemetryResult struct {
		Frame unit.Frame `json:"frame"`
	}
	ExecuteParams struct {
		Command  unit.Command `json:"command"`
		LevelMax int          `json:"levelMax"`
	}
	ExecuteResult struct {
		Result unit.Result `json:"result"`
	}
	ActuateParams struct {
		Command unit.Actuate `json:"command"`
	}
	ReadingsResult struct {
		Readings []unit.Reading `json:"readings"`
	}
	CloseParams struct {
		Restore bool `json:"restore"`
	}
	CancelParams struct {
		ID uint64 `json:"id"`
	}
)

// A CodedError is an error the helper raises on its own account, with a
// wire code of its own: CodeNoDriver when no device is open, CodeUnsupported
// for a method the family or the device does not have.
type CodedError struct {
	Code    string
	Message string
}

func (e *CodedError) Error() string { return "helper: " + e.Code + ": " + e.Message }

// ErrExited says the helper process ended (or never started) while a call
// was waiting on it. Driver calls report it as a loss (the unit stopped
// answering, as far as the connector can tell).
var ErrExited = errors.New("helper: the helper program ended")

// ToWire turns a Go error into its wire form, keeping the connector's own
// errors recognizable on the other side.
func ToWire(err error) *WireError {
	var loss *unit.LossError
	var coded *CodedError
	switch {
	case errors.As(err, &coded):
		return &WireError{Code: coded.Code, Message: coded.Message}
	case errors.As(err, &loss):
		return &WireError{Code: CodeLoss, Message: err.Error(), Reason: loss.Reason}
	case errors.Is(err, unit.ErrNoDevice):
		return &WireError{Code: CodeNoDevice, Message: err.Error()}
	case errors.Is(err, unit.ErrCancelled):
		return &WireError{Code: CodeCancelled, Message: err.Error()}
	case errors.Is(err, unit.ErrArmed):
		return &WireError{Code: CodeArmed, Message: err.Error()}
	case errors.Is(err, unit.ErrNoActuator):
		return &WireError{Code: CodeNoActuator, Message: err.Error()}
	}
	return &WireError{Code: CodeError, Message: err.Error()}
}

// FromWire is ToWire's inverse.
func FromWire(e *WireError) error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case CodeLoss:
		return &unit.LossError{Reason: e.Reason, Err: errors.New(e.Message)}
	case CodeNoDevice:
		// The message is most often ErrNoDevice's own text with a detail
		// after it; wrapping keeps errors.Is without saying it twice.
		if detail, ok := strings.CutPrefix(e.Message, unit.ErrNoDevice.Error()); ok {
			detail = strings.TrimPrefix(detail, ": ")
			if detail == "" {
				return unit.ErrNoDevice
			}
			return fmt.Errorf("%w: %s", unit.ErrNoDevice, detail)
		}
		return fmt.Errorf("%w: %s", unit.ErrNoDevice, e.Message)
	case CodeCancelled:
		return unit.ErrCancelled
	case CodeArmed:
		return unit.ErrArmed
	case CodeNoActuator:
		return fmt.Errorf("%w: %s", unit.ErrNoActuator, strings.TrimPrefix(strings.TrimPrefix(e.Message, unit.ErrNoActuator.Error()), ": "))
	case CodeNoDriver, CodeUnsupported:
		return &CodedError{Code: e.Code, Message: e.Message}
	}
	return fmt.Errorf("helper: %s: %s", e.Code, e.Message)
}

// MustJSON marshals v; nil for nil or for a value that cannot be marshaled
// (an answer without a result, which the reader treats as none).
func MustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
