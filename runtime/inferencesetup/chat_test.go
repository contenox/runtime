package inferencesetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func toolProbeResult(messages []modelrepo.Message) modelrepo.ChatResult {
	last := messages[len(messages)-1]
	if last.Role == "tool" {
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: last.Content}}
	}
	call := modelrepo.ToolCall{ID: "setup-call", Type: "function", ProviderMeta: map[string]string{"signature": "must-survive"}}
	call.Function.Name, call.Function.Arguments = "contenox_ready", `{"status":"ok"}`
	return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant"}, ToolCalls: []modelrepo.ToolCall{call}}
}

func toolProbeHTTPResponse(t *testing.T, r *http.Request) any {
	t.Helper()
	var request struct {
		Messages []modelrepo.Message `json:"messages"`
		Tools    []modelrepo.Tool    `json:"tools"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
	require.NotEmpty(t, request.Messages)
	require.Len(t, request.Tools, 1)
	require.Equal(t, "contenox_ready", request.Tools[0].Function.Name)
	result := toolProbeResult(request.Messages)
	result.Message.ToolCalls = result.ToolCalls
	return map[string]any{"choices": []map[string]any{{"message": result.Message, "finish_reason": "stop"}}}
}

func TestUnit_Roles_ToolVerificationRejectsInvalidCallsAndContinuations(t *testing.T) {
	for _, outcome := range []string{"valid", "prose", "wrong-tool", "missing-id", "invalid-json", "wrong-arguments", "extra-arguments", "extra-call", "wrong-result", "repeated-call", "continuation-error", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			err := inferencesetup.VerifyToolUse(ctx, func(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
				calls++
				config := &modelrepo.ChatConfig{}
				for _, arg := range args {
					arg.Apply(config)
				}
				require.Len(t, config.Tools, 1)
				result := toolProbeResult(messages)
				if calls == 2 {
					require.Len(t, messages, 3)
					require.Equal(t, "must-survive", messages[1].ToolCalls[0].ProviderMeta["signature"])
					require.Equal(t, messages[1].ToolCalls[0].ID, messages[2].ToolCallID)
					switch outcome {
					case "wrong-result":
						result.Message.Content = "OK"
					case "repeated-call":
						result = toolProbeResult(messages[:1])
					case "continuation-error":
						return result, errors.New("provider unavailable")
					}
					return result, nil
				}
				switch outcome {
				case "prose":
					result = modelrepo.ChatResult{Message: modelrepo.Message{Content: "OK"}}
				case "wrong-tool":
					result.ToolCalls[0].Function.Name = "unexpected"
				case "missing-id":
					result.ToolCalls[0].ID = ""
				case "invalid-json":
					result.ToolCalls[0].Function.Arguments = "{"
				case "wrong-arguments":
					result.ToolCalls[0].Function.Arguments = `{"status":"failed"}`
				case "extra-arguments":
					result.ToolCalls[0].Function.Arguments = `{"status":"ok","other":true}`
				case "extra-call":
					result.ToolCalls = append(result.ToolCalls, result.ToolCalls[0])
				case "cancel":
					cancel()
				}
				return result, nil
			})
			if outcome == "valid" {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
			} else {
				require.Error(t, err)
			}
		})
	}
}
