package modelrepo

import (
	"testing"
)

func TestUnit_GeminiModelSupportsVision(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"gemini-2.5-pro", true},
		{"gemini-2.5-flash-lite", true},
		{"gemini-3.1-pro-preview", true},
		{"gemini-1.5-pro", true},
		{"gemini-pro-vision", true},
		{"models/gemini-2.5-pro", true},
		{"publishers/google/models/gemini-3-flash-preview", true},
		{"gemini-2.5-flash-preview-tts", false},
		{"gemini-embedding-001", false},
		{"gemini-2.5-flash-live-translate-preview", false},
		{"lyria-3-pro-preview", false},
		{"veo-3.1-generate-preview", false},
		{"imagen-4.0-generate", false},
		{"text-embedding-004", false},
		{"some-random-model", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := GeminiModelSupportsVision(tc.name); got != tc.want {
			t.Errorf("GeminiModelSupportsVision(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
