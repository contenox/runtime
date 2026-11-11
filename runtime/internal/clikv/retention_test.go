package clikv

import (
	"testing"
	"time"
)

func TestUnit_ParseEventRetention(t *testing.T) {
	for in, want := range map[string]time.Duration{"30": 30 * 24 * time.Hour, " 7 ": 7 * 24 * time.Hour} {
		got, on, err := ParseEventRetention(in)
		if err != nil || !on || got != want {
			t.Fatalf("ParseEventRetention(%q) = %v, %v, %v", in, got, on, err)
		}
	}
	for _, in := range []string{"", "off", "OFF", "0"} {
		if _, on, err := ParseEventRetention(in); err != nil || on {
			t.Fatalf("ParseEventRetention(%q) should switch pruning off, got on=%v err=%v", in, on, err)
		}
	}
	for _, in := range []string{"-1", "a week", "1.5"} {
		if _, _, err := ParseEventRetention(in); err == nil {
			t.Fatalf("ParseEventRetention(%q) accepted a bad value", in)
		}
	}
}
