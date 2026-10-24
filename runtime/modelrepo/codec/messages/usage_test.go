package messages_test

import (
	"testing"

	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Usage_ThinkingTokensFromNonStreamingResponse(t *testing.T) {
	raw := []byte(`{
		"role": "assistant",
		"stop_reason": "end_turn",
		"content": [{"type": "text", "text": "ok"}],
		"usage": {"input_tokens": 10, "output_tokens": 200, "output_tokens_details": {"thinking_tokens": 150}}
	}`)

	res, err := msgcodec.DecodeResponse(raw, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 150, res.Usage.ThinkingTokens, "billed reasoning tokens must be reported")
	assert.Equal(t, 200, res.Usage.CompletionTokens)
}

func TestUnit_Usage_ThinkingTokensFromFinalStreamEvent(t *testing.T) {
	d := msgcodec.NewStreamDecoder(nil)
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":200,"output_tokens_details":{"thinking_tokens":150}}}`,
		`{"type":"message_stop"}`,
	}
	for _, ev := range events {
		_, err := d.DecodeLine([]byte(ev))
		require.NoError(t, err)
	}

	term := d.Finish()
	require.NotNil(t, term.Terminal)
	require.NotNil(t, term.Terminal.Usage)
	assert.Equal(t, 150, term.Terminal.Usage.ThinkingTokens, "the breakdown arrives on the final message_delta event")
	assert.Equal(t, 200, term.Terminal.Usage.CompletionTokens)
	assert.True(t, term.Terminal.Terminated)
}

func TestUnit_Usage_CacheCreationBreakdownIsPreferred(t *testing.T) {
	raw := []byte(`{
		"role": "assistant",
		"stop_reason": "end_turn",
		"content": [{"type": "text", "text": "ok"}],
		"usage": {
			"input_tokens": 2048,
			"output_tokens": 503,
			"cache_read_input_tokens": 1800,
			"cache_creation_input_tokens": 248,
			"cache_creation": {"ephemeral_5m_input_tokens": 148, "ephemeral_1h_input_tokens": 100}
		}
	}`)

	res, err := msgcodec.DecodeResponse(raw, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 248, res.Usage.CacheWriteTokens, "the breakdown sums to the reported write total")
	assert.Equal(t, 2048+1800+248, res.Usage.PromptTokens)
}

func TestUnit_Usage_CacheCreationBreakdownAloneStillCounts(t *testing.T) {
	raw := []byte(`{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],
		"usage":{"input_tokens":100,"output_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30}}}`)
	res, err := msgcodec.DecodeResponse(raw, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 50, res.Usage.CacheWriteTokens, "a response that reports only the breakdown still counts its writes")
	assert.Equal(t, 150, res.Usage.PromptTokens)
}

func TestUnit_DecodeResponse_EmptyTurnIsNotAnError(t *testing.T) {
	t.Run("thinking only, truncated by max_tokens", func(t *testing.T) {
		raw := []byte(`{
			"role": "assistant",
			"stop_reason": "max_tokens",
			"content": [{"type": "thinking", "thinking": "still reasoning", "signature": "sig"}]
		}`)

		res, err := msgcodec.DecodeResponse(raw, nil)
		require.NoError(t, err, "the vendor's own stop reason is an outcome, not a failure")
		assert.Empty(t, res.Message.Content)
		assert.Equal(t, "still reasoning", res.Message.Thinking)
		assert.Equal(t, "max_tokens", res.FinishReason)
	})

	t.Run("tool call without text", func(t *testing.T) {
		raw := []byte(`{
			"role": "assistant",
			"stop_reason": "tool_use",
			"content": [{"type": "tool_use", "id": "tu_1", "name": "fs_list", "input": {"path": "/"}}]
		}`)

		res, err := msgcodec.DecodeResponse(raw, nil)
		require.NoError(t, err)
		assert.Empty(t, res.Message.Content)
		require.Len(t, res.ToolCalls, 1)
		assert.Equal(t, "tool_use", res.FinishReason)
	})
}
