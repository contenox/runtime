package bedrock_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
)

const novaPro = "amazon.nova-pro-v1:0"

func TestUnit_BedrockNovaThinkingTags_ChatMovesThemIntoThinking(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("<thinking> I need to read the README.md file first. </thinking>\n\nThe project builds software with AI agents.", "end_turn"))}
	provider := newWireProvider(novaPro, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "What does this project do?"}})
	require.NoError(t, err)
	require.Equal(t, "I need to read the README.md file first.", result.Message.Thinking)
	require.Equal(t, "The project builds software with AI agents.", result.Message.Content)

	transport = &wireTransport{handler: replyJSON(textOut("Plain answer, no tag <thinking>kept</thinking>", "end_turn"))}
	provider = newWireProvider(novaPro, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err = chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, "", result.Message.Thinking)
	require.Equal(t, "Plain answer, no tag <thinking>kept</thinking>", result.Message.Content, "only text that starts with the tag is split")
}

func TestUnit_BedrockNovaThinkingTags_StreamRelaysThemAsThinkingParcels(t *testing.T) {
	provider := newWireProvider(novaPro, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}, &wireTransport{handler: streamResponse(
		eventFrame("messageStart", map[string]any{"role": "assistant"}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "<thin"}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "king> plan"}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "ning </thi"}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "nking>\n\nThe "}}),
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "answer."}}),
		eventFrame("contentBlockStop", map[string]any{"contentBlockIndex": 0}),
		eventFrame("messageStop", map[string]any{"stopReason": "end_turn"}),
	)})
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, " planning ", result.Thinking)
	require.Equal(t, "The answer.", result.Content)
	require.Equal(t, "end_turn", result.FinishReason)
}
