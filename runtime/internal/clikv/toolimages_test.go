package clikv

import "testing"

func TestUnit_ParseToolImagesKept(t *testing.T) {
	for in, want := range map[string]int{"": 0, " ": 0, "0": 0, "3": 3, " 12 ": 12} {
		got, err := ParseToolImagesKept(in)
		if err != nil || got != want {
			t.Fatalf("ParseToolImagesKept(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"-1", "two", "1.5"} {
		if _, err := ParseToolImagesKept(in); err == nil {
			t.Fatalf("ParseToolImagesKept(%q) accepted a bad value", in)
		}
	}
}
