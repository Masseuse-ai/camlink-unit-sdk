package unit_test

import (
	"testing"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

func TestKindValid(t *testing.T) {
	for _, k := range []unit.Kind{"lovense", "dglabs-coyote", "mk312bt", "a", "estim-2b"} {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	for _, k := range []unit.Kind{"", "Lovense", "-x", "1abc", "a b", "a_b", "a/b", unit.Kind(make([]byte, 65))} {
		if k.Valid() {
			t.Errorf("%q should not be valid", k)
		}
	}
}

func TestIdentityLabels(t *testing.T) {
	// The one-string label composes the three parts; the tag in
	// parentheses only when there is one.
	cases := []struct {
		id   unit.Identity
		want string
	}{
		{unit.Identity{Maker: "Mastogo", Model: "Wireless TENS", Tag: "G-12AB"}, "Mastogo Wireless TENS (G-12AB)"},
		{unit.Identity{Maker: "E-Stim Systems", Model: "2B"}, "E-Stim Systems 2B"},
		{unit.Identity{Model: "2B", Tag: "COM5"}, "2B (COM5)"},
		{unit.Identity{Tag: "4A56"}, "4A56"},
		{unit.Identity{}, ""},
	}
	for _, c := range cases {
		if got := unit.LabelOf(c.id); got != c.want {
			t.Errorf("LabelOf(%+v) = %q, want %q", c.id, got, c.want)
		}
	}
	// Read back out of a label, for a helper that predates the parts: the
	// trailing parenthesis is the tag, the rest the model, no maker.
	from := []struct {
		label string
		want  unit.Identity
	}{
		{"DG-Lab Coyote 3.0 (7C3B)", unit.Identity{Model: "DG-Lab Coyote 3.0", Tag: "7C3B"}},
		{"E-Stim Systems 2B (cu.usbserial-FTCILMWQ)", unit.Identity{Model: "E-Stim Systems 2B", Tag: "cu.usbserial-FTCILMWQ"}},
		{"E-Stim Systems 2B", unit.Identity{Model: "E-Stim Systems 2B"}},
		{"  Some unit  ", unit.Identity{Model: "Some unit"}},
		{"(odd)", unit.Identity{Model: "(odd)"}},
		{"", unit.Identity{}},
	}
	for _, c := range from {
		if got := unit.IdentityFromLabel(c.label); got != c.want {
			t.Errorf("IdentityFromLabel(%q) = %+v, want %+v", c.label, got, c.want)
		}
	}
}

func TestDefaultSettingsFor(t *testing.T) {
	if got := unit.DefaultSettingsFor(unit.Capabilities{}); got != unit.DefaultSettings() {
		t.Fatalf("no caps: %+v", got)
	}
	twoRanges := unit.Capabilities{LevelMax: 99, PowerModes: []string{unit.PowerModeNormal, unit.PowerModeHigh}}
	if got := unit.DefaultSettingsFor(twoRanges); got.PowerMode != unit.PowerModeHigh || got.LevelMax != unit.DefaultLevelCap {
		t.Fatalf("two ranges: %+v", got)
	}
	oneRange := unit.Capabilities{LevelMax: 25, LevelMaxDefault: 15}
	if got := unit.DefaultSettingsFor(oneRange); got.PowerMode != unit.PowerModeNormal || got.LevelMax != 15 {
		t.Fatalf("one range: %+v", got)
	}
	if got := unit.LevelMaxFor(unit.Capabilities{LevelMax: 20}, unit.Settings{LevelMax: 50}); got != 20 {
		t.Fatalf("LevelMaxFor: %d", got)
	}
}
