package agentservice

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/taskengine"
)

func TestUnit_InferStopReason_FinishReason(t *testing.T) {
	step := func(fr string) taskengine.CapturedStateUnit {
		return taskengine.CapturedStateUnit{FinishReason: fr}
	}
	cases := []struct {
		name  string
		err   error
		steps []taskengine.CapturedStateUnit
		want  StopReason
	}{
		{"no steps", nil, nil, StopEndTurn},
		{"stop", nil, []taskengine.CapturedStateUnit{step("stop")}, StopEndTurn},
		{"length", nil, []taskengine.CapturedStateUnit{step("length")}, StopMaxTokens},
		{"max_tokens", nil, []taskengine.CapturedStateUnit{step("max_tokens")}, StopMaxTokens},
		{"max_output_tokens", nil, []taskengine.CapturedStateUnit{step("MAX_OUTPUT_TOKENS")}, StopMaxTokens},
		{"model_length", nil, []taskengine.CapturedStateUnit{step("model_length")}, StopMaxTokens},
		{"last model step decides", nil, []taskengine.CapturedStateUnit{step("length"), step("stop")}, StopEndTurn},
		{"skips empty trailing", nil, []taskengine.CapturedStateUnit{step("length"), step("")}, StopMaxTokens},
		{"cancel wins", context.Canceled, []taskengine.CapturedStateUnit{step("length")}, StopCancelled},
		{"error text wins", errors.New("token limit"), nil, StopMaxTokens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferStopReason(tc.err, tc.steps); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
