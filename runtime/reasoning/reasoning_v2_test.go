package reasoning_test

import (
	"github.com/contenox/runtime/runtime/reasoning"
)

import "testing"

func TestUnit_NormalizeThinkLevelsAndAliases_V2(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"auto", reasoning.Auto},
		{"AUTO", reasoning.Auto},
		{"default", reasoning.Auto},
		{"off", reasoning.Off},
		{"none", reasoning.Off},
		{"disabled", reasoning.Off},
		{"false", reasoning.Off},
		{"minimal", reasoning.Minimal},
		{"low", reasoning.Low},
		{"medium", reasoning.Medium},
		{"high", reasoning.High},
		{"xhigh", reasoning.XHigh},
		{"true", reasoning.High},
		{"on", reasoning.High},
	}
	for _, tc := range cases {
		got, err := reasoning.Normalize(tc.in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnit_NormalizeOptionalAndDisplay_V2(t *testing.T) {
	if _, ok, err := reasoning.NormalizeOptional("   "); err != nil || ok {
		t.Fatalf("NormalizeOptional(blank) = ok %v err %v, want ok false nil", ok, err)
	}
	if reasoning.DisplayEnabled(reasoning.Off) {
		t.Fatal("off must not display thinking")
	}
	if reasoning.DisplayEnabled(reasoning.Auto) {
		t.Fatal("auto must not force display thinking")
	}
	if !reasoning.DisplayEnabled(reasoning.High) {
		t.Fatal("high must display thinking")
	}
	if _, err := reasoning.Normalize("wat"); err == nil {
		t.Fatal("invalid levels must error")
	}
}

func TestUnit_Normalize_ReachesTheHighestLevelAndOff(t *testing.T) {
	for _, in := range []string{"max", "MAX", " max "} {
		got, err := reasoning.Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", in, err)
		}
		if got != reasoning.Max {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, reasoning.Max)
		}
	}
	off, err := reasoning.Normalize("off")
	if err != nil || off != reasoning.Off {
		t.Fatalf("off = %q err %v", off, err)
	}
}

func TestUnit_Levels_AreAscendingAndEndAtMax(t *testing.T) {
	if len(reasoning.Levels) == 0 {
		t.Fatal("the level vocabulary must not be empty")
	}
	if reasoning.Levels[0] != reasoning.Off {
		t.Fatalf("the lowest level is off, got %q", reasoning.Levels[0])
	}
	if reasoning.Levels[len(reasoning.Levels)-1] != reasoning.Max {
		t.Fatalf("the highest level is max, got %q", reasoning.Levels[len(reasoning.Levels)-1])
	}
	for _, level := range reasoning.Levels {
		if _, err := reasoning.Normalize(level); err != nil {
			t.Fatalf("enumerated level %q is not accepted by Normalize: %v", level, err)
		}
	}
	if reasoning.Max == reasoning.XHigh {
		t.Fatal("the highest level must be distinct from xhigh")
	}
}

func TestUnit_ResolveForWire_SaysNothingForUnsetAutoAndOff(t *testing.T) {
	for _, in := range []string{"", "auto", "default"} {
		got, err := reasoning.ResolveForWire(in)
		if err != nil {
			t.Fatalf("ResolveForWire(%q): %v", in, err)
		}
		if got != "" {
			t.Fatalf("ResolveForWire(%q) = %q, want empty", in, got)
		}
	}
	if got, err := reasoning.ResolveForWire("none"); err != nil || got != reasoning.Off {
		t.Fatalf("ResolveForWire(none) = %q err %v, want %q", got, err, reasoning.Off)
	}
	for _, in := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		got, err := reasoning.ResolveForWire(in)
		if err != nil {
			t.Fatalf("ResolveForWire(%q): %v", in, err)
		}
		if got != in {
			t.Fatalf("ResolveForWire(%q) = %q", in, got)
		}
	}
	if _, err := reasoning.ResolveForWire("very-much"); err == nil {
		t.Fatal("an unknown level must be refused")
	}
}

func TestUnit_DisplayEnabled_ExcludesOffAndAutoOnly(t *testing.T) {
	if reasoning.DisplayEnabled(reasoning.Off) || reasoning.DisplayEnabled(reasoning.Auto) {
		t.Fatal("off and auto do not enable reasoning")
	}
	if reasoning.DisplayEnabled("") {
		t.Fatal("an unset level does not enable reasoning")
	}
	for _, level := range []string{reasoning.Minimal, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max} {
		if !reasoning.DisplayEnabled(level) {
			t.Fatalf("%q must enable reasoning", level)
		}
	}
}
