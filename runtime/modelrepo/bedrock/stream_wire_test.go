package bedrock_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func streamTextProvider(t *testing.T, frames [][]byte, capabilities modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	return newWireProvider(claudeSonnet45, capabilities, &wireTransport{handler: streamResponse(frames...)})
}

func TestUnit_BedrockConverseStream_RelaysEventsInOrder(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("messageStart", map[string]any{"role": "assistant"}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{
			"reasoningContent": map[string]any{"text": "pondering"},
		}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "let me "}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "check"}}),
		eventFrame("contentBlockStart", map[string]any{"contentBlockIndex": 1, "start": map[string]any{
			"toolUse": map[string]any{"toolUseId": "tooluse_1", "name": "fs_list"},
		}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 1, "delta": map[string]any{
			"toolUse": map[string]any{"input": `{"path":`},
		}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 1, "delta": map[string]any{
			"toolUse": map[string]any{"input": `"/x"}`},
		}}),
		eventFrame("contentBlockStop", map[string]any{"contentBlockIndex": 1}),
		eventFrame("messageStop", map[string]any{"stopReason": "tool_use"}),
		eventFrame("metadata", map[string]any{"usage": map[string]any{"inputTokens": 31, "outputTokens": 13, "totalTokens": 44}}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})

	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "list /x"}},
		modelrepo.WithTools(toolSpec("fs.list", "list files", objectSchema())))
	require.NoError(t, err)
	require.Equal(t, "let me check", result.Content)
	require.Equal(t, "pondering", result.Thinking)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "tooluse_1", result.ToolCalls[0].ID)
	require.Equal(t, "fs.list", result.ToolCalls[0].Function.Name)
	require.Equal(t, `{"path":"/x"}`, result.ToolCalls[0].Function.Arguments)
	require.Equal(t, "tool_use", result.FinishReason)
	require.NotNil(t, result.Usage)
	require.Equal(t, 31, result.Usage.PromptTokens)
	require.Equal(t, 13, result.Usage.CompletionTokens)
	require.Equal(t, 44, result.Usage.TotalTokens)
}

func TestUnit_BedrockConverseStream_TruncatedStreamIsNotATurn(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "partial"}}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	_, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.ErrorIs(t, err, modelrepo.ErrStreamTruncated)
}

func TestUnit_BedrockConverseStream_ReportsTheMatchedStopSequence(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "SUCCESS"}}),
		eventFrame("messageStop", map[string]any{
			"stopReason":                    "stop_sequence",
			"additionalModelResponseFields": map[string]any{"stop_sequence": "SUCCESS"},
		}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithStopSequences("SUCCESS", "FAILURE"))
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", result.FinishReason)
	require.Equal(t, "SUCCESS", result.StopSequence)
}

func TestUnit_BedrockConverseStream_RefusalIsAnOutcomeNotAnAnswer(t *testing.T) {
	for _, reason := range []string{"guardrail_intervened", "content_filtered"} {
		t.Run(reason, func(t *testing.T) {
			provider := streamTextProvider(t, [][]byte{
				eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "blocked content"}}),
				eventFrame("messageStop", map[string]any{"stopReason": reason}),
			}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
			_, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
			require.ErrorIs(t, err, modelrepo.ErrRefused)
			var refusal *modelrepo.RefusalError
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, "bedrock", refusal.Provider)
			require.Equal(t, reason, refusal.Code)
			require.Equal(t, "blocked content", refusal.Text)
		})
	}
}

func TestUnit_BedrockConverseStream_ContextWindowTerminalIsAnOverflow(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("messageStop", map[string]any{"stopReason": "model_context_window_exceeded"}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, "model_context_window_exceeded", result.FinishReason)
}

func TestUnit_BedrockConverseStream_ReplaysTheStreamedReasoningSignature(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{
			"reasoningContent": map[string]any{"text": "weighing it"},
		}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{
			"reasoningContent": map[string]any{"signature": "sig-stream"},
		}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "answer"}}),
		eventFrame("messageStop", map[string]any{"stopReason": "end_turn"}),
	}, adaptiveOnlyCapabilities("high"))
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "why"}})
	require.NoError(t, err)
	require.Equal(t, "answer", result.Content)
	require.Equal(t, "weighing it", result.Thinking)
	require.NotNil(t, result.Continuation)

	transport := &wireTransport{handler: replyJSON(textOut("done", "end_turn"))}
	next := newWireProvider(claudeSonnet45, adaptiveOnlyCapabilities("high"), transport)
	_, err = chatOnce(t, next, []modelrepo.Message{
		{Role: "user", Content: "why"},
		{Role: "assistant", Content: result.Content, Thinking: result.Thinking, Continuation: result.Continuation},
		{Role: "user", Content: "go on"},
	})
	require.NoError(t, err)
	assistant := transport.mustBody(t)["messages"].([]any)[1].(map[string]any)["content"].([]any)
	reasoning := assistant[0].(map[string]any)["reasoningContent"].(map[string]any)["reasoningText"].(map[string]any)
	require.Equal(t, "weighing it", reasoning["text"])
	require.Equal(t, "sig-stream", reasoning["signature"])
}

func TestUnit_BedrockConverseStream_ClassifiesExceptionEvents(t *testing.T) {
	cases := []struct {
		name     string
		frame    []byte
		sentinel error
	}{
		{"throttling", exceptionFrame("throttlingException", map[string]any{"message": "Too many requests"}), modelrepo.ErrRateLimited},
		{"service unavailable", exceptionFrame("serviceUnavailableException", map[string]any{"message": "Service is unavailable"}), nil},
		{"validation", exceptionFrame("validationException", map[string]any{"message": "Input is too long for requested model."}), modelrepo.ErrContextLengthExceeded},
		{"model stream error", exceptionFrame("modelStreamErrorException", map[string]any{"message": "stream broke"}), nil},
		{"internal server", exceptionFrame("internalServerException", map[string]any{"message": "internal failure"}), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			provider := streamTextProvider(t, [][]byte{
				eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "partial"}}),
				c.frame,
			}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
			_, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
			require.Error(t, err, "an exception event must not read as a completed turn")
			if c.sentinel != nil {
				require.ErrorIs(t, err, c.sentinel)
			}
			require.False(t, errors.Is(err, modelrepo.ErrStreamTruncated), "a reported failure is not a truncation")
		})
	}
}

func TestUnit_BedrockConverseStream_EmptyTerminalIsATurn(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("messageStop", map[string]any{"stopReason": "end_turn"}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Empty(t, result.Content)
	require.Equal(t, "end_turn", result.FinishReason)
}

func TestUnit_BedrockConverseStream_RefusedRequestIsClassified(t *testing.T) {
	transport := &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
		return errorResponse(request, http.StatusTooManyRequests, "ThrottlingException", "Too many requests")
	}}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}, transport)
	connection, err := provider.GetStreamConnection(t.Context(), "bedrock:us-east-1")
	require.NoError(t, err)
	_, err = connection.Stream(t.Context(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.ErrorIs(t, err, modelrepo.ErrRateLimited)
}
