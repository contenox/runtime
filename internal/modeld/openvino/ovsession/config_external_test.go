package ovsession_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

func TestUnit_GenAIConfig_SparseAttentionRequiresExplicitChoice(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name   string
		choice *bool
		want   bool
	}{
		{name: "absent retains upstream dense default"},
		{name: "explicit dense", choice: &off},
		{name: "explicit sparse", choice: &on, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ovsession.GenAIConfig{UseSparseAttention: tc.choice}
			if got := cfg.SparseAttentionEnabled(); got != tc.want {
				t.Fatalf("SparseAttentionEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
