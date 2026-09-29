package unit_test

import (
	"encoding/json"
	"testing"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

// FuzzParseCommand: a command of any bytes never panics, an accepted
// command carries an allowed verb and channel, and CheckCaps refuses every
// accepted actuation that steps outside the connector's caps.
func FuzzParseCommand(f *testing.F) {
	f.Add([]byte(`{"verb":"set_level","channel":"a","level":6}`))
	f.Add([]byte(`{"verb":"set_mode","mode":118}`))
	f.Add([]byte(`{"verb":"adjust_ma","delta":-10,"reason":"x"}`))
	f.Add([]byte(`{"verb":"status"}`))
	f.Add([]byte(`{"verb":"set_level","channel":"b","level":1}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	caps := unit.Capabilities{LevelMax: 99, Channels: []string{"a"}, Modes: []int{0x76, 0x77}, Tempo: true}
	f.Fuzz(func(t *testing.T, raw []byte) {
		cmd, err := unit.ParseCommand(json.RawMessage(raw))
		if err != nil {
			return
		}
		if cmd.Channel != "" && cmd.Channel != "a" {
			t.Fatalf("channel %q accepted", cmd.Channel)
		}
		capsErr := unit.CheckCaps(cmd, caps, unit.DefaultSettings())
		switch cmd.Verb {
		case "status", "release":
			if capsErr != nil {
				t.Fatalf("%s refused: %v", cmd.Verb, capsErr)
			}
		case "set_level":
			if cmd.Level != nil && (*cmd.Level < 0 || *cmd.Level > unit.DefaultLevelCap) && capsErr == nil {
				t.Fatalf("level %d accepted", *cmd.Level)
			}
			// A session's maximum bounds it the same way, never past the scale.
			for _, levelMax := range []int{0, 40, 99} {
				err := unit.CheckCaps(cmd, caps, unit.Settings{PowerMode: "normal", LevelMax: levelMax})
				if cmd.Level != nil && (*cmd.Level < 0 || *cmd.Level > levelMax) && err == nil {
					t.Fatalf("level %d accepted under a maximum of %d", *cmd.Level, levelMax)
				}
			}
		case "adjust_level":
			if cmd.Delta != nil && (*cmd.Delta < -unit.LevelDeltaCap || *cmd.Delta > unit.LevelDeltaCap) && capsErr == nil {
				t.Fatalf("delta %d accepted", *cmd.Delta)
			}
		case "set_ma":
			if cmd.Percent != nil && (*cmd.Percent < 0 || *cmd.Percent > unit.TempoPercentCap) && capsErr == nil {
				t.Fatalf("percent %d accepted", *cmd.Percent)
			}
		case "adjust_ma":
			if cmd.Delta != nil && (*cmd.Delta < -unit.TempoDeltaCap || *cmd.Delta > unit.TempoDeltaCap) && capsErr == nil {
				t.Fatalf("tempo delta %d accepted", *cmd.Delta)
			}
		case "set_mode":
			if cmd.Mode != nil && *cmd.Mode != 0x76 && *cmd.Mode != 0x77 && capsErr == nil {
				t.Fatalf("mode %d accepted", *cmd.Mode)
			}
		default:
			t.Fatalf("verb %q accepted", cmd.Verb)
		}
	})
}

// FuzzParseSettings: settings of any bytes never panic, and what is
// accepted names one of the two power ranges with a maximum on the scale.
func FuzzParseSettings(f *testing.F) {
	f.Add([]byte(`{"type":"device_settings","sessionId":"s","powerMode":"high","levelMax":85}`))
	f.Add([]byte(`{"powerMode":"normal","levelMax":0}`))
	f.Add([]byte(`{"powerMode":"low","levelMax":50}`))
	f.Add([]byte(`{"powerMode":"high","levelMax":100}`))
	f.Add([]byte(`{"powerMode":"high"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := unit.ParseSettings(json.RawMessage(raw))
		if err != nil {
			return
		}
		if s.PowerMode != unit.PowerModeNormal && s.PowerMode != unit.PowerModeHigh {
			t.Fatalf("power mode %q accepted", s.PowerMode)
		}
		if s.LevelMax < 0 || s.LevelMax > unit.LevelScaleMax {
			t.Fatalf("levelMax %d accepted", s.LevelMax)
		}
		if s.Validate() != nil {
			t.Fatalf("parsed settings do not validate: %+v", s)
		}
	})
}
