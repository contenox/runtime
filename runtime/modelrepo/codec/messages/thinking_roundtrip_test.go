package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/stretchr/testify/require"
)

func TestUnit_ThinkingRoundTrip_NonStreaming(t *testing.T) {
	raw := []byte(`{
		"role": "assistant",
		"stop_reason": "tool_use",
		"content": [
			{"type": "thinking", "thinking": "I should list the files.", "signature": "sig-abc"},
			{"type": "redacted_thinking", "data": "opaque-blob"},
			{"type": "text", "text": "Let me check."},
			{"type": "tool_use", "id": "tu_1", "name": "fs_list", "input": {"path": "/"}}
		]
	}`)

	res, err := msgcodec.DecodeResponse(raw, map[string]string{"fs_list": "fs.list"})
	require.NoError(t, err)
	require.Equal(t, "I should list the files.", res.Message.Thinking)
	require.Len(t, res.ToolCalls, 1)

	meta := res.ToolCalls[0].ProviderMeta[msgcodec.ThinkingBlocksMetaKey]
	require.NotEmpty(t, meta, "thinking blocks must persist on the tool call's ProviderMeta")

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &blocks))
	require.Len(t, blocks, 2)
	require.Equal(t, "thinking", blocks[0]["type"])
	require.Equal(t, "sig-abc", blocks[0]["signature"], "signature must round-trip verbatim")
	require.Equal(t, "redacted_thinking", blocks[1]["type"])
	require.Equal(t, "opaque-blob", blocks[1]["data"])

	next := []modelrepo.Message{
		{Role: "user", Content: "list my files"},
		{Role: "assistant", Content: "Let me check.", ToolCalls: res.ToolCalls},
		{Role: "tool", ToolCallID: "tu_1", Content: "[\"a.txt\"]"},
	}
	req, _, err := msgcodec.Build(next, &modelrepo.ChatConfig{})
	require.Len(t, req.Messages, 3)

	assistant := req.Messages[1]
	require.Equal(t, "assistant", assistant.Role)
	require.GreaterOrEqual(t, len(assistant.Content), 4)
	require.Equal(t, "thinking", assistant.Content[0].Type, "thinking blocks must precede all other blocks")
	require.Equal(t, "I should list the files.", assistant.Content[0].Thinking)
	require.Equal(t, "sig-abc", assistant.Content[0].Signature)
	require.Equal(t, "redacted_thinking", assistant.Content[1].Type)
	require.Equal(t, "opaque-blob", assistant.Content[1].Data)
	require.Equal(t, "text", assistant.Content[2].Type)
	require.Equal(t, "tool_use", assistant.Content[3].Type)

	wire, err := json.Marshal(assistant.Content[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"thinking","thinking":"I should list the files.","signature":"sig-abc"}`, string(wire))
}

func TestUnit_ThinkingRoundTrip_Streaming(t *testing.T) {
	d := msgcodec.NewStreamDecoder(nil)

	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"step one"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" step two"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-xyz"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_9","name":"fs_list"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"/\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
	}

	var toolStart *modelrepo.ToolCallDelta
	var thinking string
	for _, ev := range events {
		parcels, err := d.DecodeLine([]byte(ev))
		require.NoError(t, err)
		for _, p := range parcels {
			thinking += p.Thinking
			if p.ToolCall != nil && p.ToolCall.Name != "" {
				toolStart = p.ToolCall
			}
		}
	}

	require.Equal(t, "step one step two", thinking)
	require.NotNil(t, toolStart)
	meta := toolStart.ProviderMeta[msgcodec.ThinkingBlocksMetaKey]
	require.NotEmpty(t, meta, "stream decoder must attach thinking blocks to the first tool_use delta")

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &blocks))
	require.Len(t, blocks, 1)
	require.Equal(t, "step one step two", blocks[0]["thinking"])
	require.Equal(t, "sig-xyz", blocks[0]["signature"])
}

func TestUnit_StripThinkingBlocks(t *testing.T) {
	var req msgcodec.Request
	require.NoError(t, json.Unmarshal([]byte(`{
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hi"}]},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "t", "signature": "s"},
				{"type": "tool_use", "id": "tu_1", "name": "f", "input": {}}
			]},
			{"role": "assistant", "content": [{"type": "redacted_thinking", "data": "blob"}]}
		]
	}`), &req))
	msgcodec.StripThinkingBlocks(&req)
	require.Len(t, req.Messages, 2, "a message left empty after stripping is removed")
	require.Len(t, req.Messages[1].Content, 1)
	require.Equal(t, "tool_use", req.Messages[1].Content[0].Type)
}
