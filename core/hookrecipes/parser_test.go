package hookrecipes_test

import (
	"reflect"
	"testing"

	"github.com/contenox/contenox/core/hookrecipes"
)

func TestParsePrefixedArgs(t *testing.T) {
	tests := []struct {
		input    string
		wantArgs map[string]string
		wantRem  string
		wantErr  bool
	}{
		{
			input:    "args: top_k=3, epsilon=0.1 | hello world",
			wantArgs: map[string]string{"top_k": "3", "epsilon": "0.1"},
			wantRem:  "hello world",
		},
		{
			input:    "args: position=2, radius=5| some input after",
			wantArgs: map[string]string{"position": "2", "radius": "5"},
			wantRem:  "some input after",
		},
		{
			input:    "args: position=2, radius=5.5| some input after",
			wantArgs: map[string]string{"position": "2", "radius": "5.5"},
			wantRem:  "some input after",
		},
		{
			input:    "args: something_weird=1| rest",
			wantArgs: map[string]string{"something_weird": "1"},
			wantRem:  "rest",
		},
		{
			input:    "not-an-args-input",
			wantArgs: nil,
			wantRem:  "not-an-args-input",
		},
		{
			input:   "args: top_k=3, epsilon",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		rem, args, err := hookrecipes.ParsePrefixedArgs(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("expected error for input: %q", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("unexpected error for input %q: %v", tt.input, err)
			continue
		}
		if rem != tt.wantRem {
			t.Errorf("remaining input mismatch: got %q, want %q", rem, tt.wantRem)
		}
		if !reflect.DeepEqual(args, tt.wantArgs) {
			t.Errorf("args mismatch: got %v, want %v", args, tt.wantArgs)
		}
	}
}
