package unit_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

// A command names channel A or B, or none (A); anything else is malformed.
func TestParseCommandChannels(t *testing.T) {
	for _, raw := range []string{
		`{"verb":"set_level","level":3}`,
		`{"verb":"set_level","channel":"a","level":3}`,
		`{"verb":"set_level","channel":"b","level":3}`,
		`{"verb":"adjust_level","channel":"b","delta":-2}`,
	} {
		if _, err := unit.ParseCommand(json.RawMessage(raw)); err != nil {
			t.Fatalf("%s refused: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"verb":"set_level","channel":"c","level":3}`,
		`{"verb":"set_level","channel":"A","level":3}`,
		`{"verb":"set_level","channel":"ab","level":3}`,
	} {
		_, err := unit.ParseCommand(json.RawMessage(raw))
		if err == nil || !strings.Contains(err.Error(), "channel must be") {
			t.Fatalf("%s: want a channel refusal, got %v", raw, err)
		}
	}
}

// ChannelOf reads A into a command that names no channel.
func TestChannelOf(t *testing.T) {
	if got := unit.ChannelOf(unit.Command{Verb: "set_level"}); got != unit.ChannelA {
		t.Fatalf("no channel read as %q", got)
	}
	if got := unit.ChannelOf(unit.Command{Verb: "set_level", Channel: unit.ChannelB}); got != unit.ChannelB {
		t.Fatalf("channel B read as %q", got)
	}
}

// A device lists the channels it drives; one that lists none drives A.
func TestHasChannel(t *testing.T) {
	none := unit.Capabilities{}
	if !none.HasChannel(unit.ChannelA) || none.HasChannel(unit.ChannelB) {
		t.Fatal("a device listing no channels drives A alone")
	}
	one := unit.Capabilities{Channels: []string{"a"}}
	if !one.HasChannel("a") || one.HasChannel("b") {
		t.Fatal("a one-channel device drives A alone")
	}
	two := unit.Capabilities{Channels: []string{"a", "b"}}
	if !two.HasChannel("a") || !two.HasChannel("b") || two.HasChannel("c") {
		t.Fatal("a two-channel device drives A and B")
	}
}

// CheckCaps refuses a level command for a channel the device does not
// list, before anything else about it is judged, and passes the same
// command on a device that lists the channel.
func TestCheckCapsChannels(t *testing.T) {
	one := unit.Capabilities{LevelMax: 99, Channels: []string{"a"}, Modes: []int{1}}
	two := unit.Capabilities{LevelMax: 99, Channels: []string{"a", "b"}, Modes: []int{1}}
	settings := unit.DefaultSettings()
	setB := unit.Command{Verb: "set_level", Channel: "b", Level: unit.Int(4)}
	adjustB := unit.Command{Verb: "adjust_level", Channel: "b", Delta: unit.Int(1)}
	for _, cmd := range []unit.Command{setB, adjustB} {
		err := unit.CheckCaps(cmd, one, settings)
		if err == nil || !strings.Contains(err.Error(), "channel b is not driven") {
			t.Fatalf("%s on B accepted by a one-channel device: %v", cmd.Verb, err)
		}
		if err := unit.CheckCaps(cmd, two, settings); err != nil {
			t.Fatalf("%s on B refused by a two-channel device: %v", cmd.Verb, err)
		}
	}
	// The cap is the box's: channel B is bounded by the same maximum.
	high := unit.Command{Verb: "set_level", Channel: "b", Level: unit.Int(unit.DefaultLevelCap + 1)}
	if err := unit.CheckCaps(high, two, settings); err == nil {
		t.Fatal("a level past the cap accepted on channel B")
	}
	// A command naming no channel is A's, on either device.
	setA := unit.Command{Verb: "set_level", Level: unit.Int(4)}
	for _, caps := range []unit.Capabilities{one, two} {
		if err := unit.CheckCaps(setA, caps, settings); err != nil {
			t.Fatalf("set_level without a channel refused: %v", err)
		}
	}
	// A mode or a tempo command has no channel to judge.
	if err := unit.CheckCaps(unit.Command{Verb: "set_mode", Channel: "b", Mode: unit.Int(1)}, one, settings); err != nil {
		t.Fatalf("set_mode judged by its channel: %v", err)
	}
}

// The result of a level command names its channel on the wire, and a
// two-channel frame carries channel B's level.
func TestChannelOnTheWire(t *testing.T) {
	res, err := json.Marshal(unit.Result{Verb: "set_level", Channel: "b", Level: unit.Int(7)})
	if err != nil || !strings.Contains(string(res), `"channel":"b"`) {
		t.Fatalf("result without its channel: %s %v", res, err)
	}
	plain, _ := json.Marshal(unit.Result{Verb: "set_level", Level: unit.Int(7)})
	if strings.Contains(string(plain), "channel") {
		t.Fatalf("a result with no channel carries the field: %s", plain)
	}
	frame, _ := json.Marshal(unit.Frame{LevelA: unit.Int(3), LevelB: unit.Int(5)})
	if !strings.Contains(string(frame), `"levelB":5`) {
		t.Fatalf("frame without channel B: %s", frame)
	}
	single, _ := json.Marshal(unit.Frame{LevelA: unit.Int(3)})
	if strings.Contains(string(single), "levelB") {
		t.Fatalf("a single-channel frame carries levelB: %s", single)
	}
}
