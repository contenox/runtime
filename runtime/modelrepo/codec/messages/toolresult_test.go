package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolResultWireBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Content   any    `json:"content"`
}

func wireToolResult(t *testing.T, m modelrepo.Message) toolResultWireBlock {
	t.Helper()
	req, _, err := msgcodec.Build([]modelrepo.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{tc("tu_1", "fs.list", `{}`)}},
		m,
	}, &modelrepo.ChatConfig{})
	require.NoError(t, err)

	var body struct {
		Messages []struct {
			Role    string                `json:"role"`
			Content []toolResultWireBlock `json:"content"`
		} `json:"messages"`
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Messages, 3)

	block := body.Messages[2].Content[0]
	require.Equal(t, "tool_result", block.Type)
	return block
}

func TestUnit_Build_ToolResultCarriesIsError(t *testing.T) {
	failed := wireToolResult(t, modelrepo.Message{
		Role: "tool", ToolCallID: "tu_1", Content: "permission denied", ToolResultError: true,
	})
	assert.True(t, failed.IsError, "a failed call must be a result the model can tell from an empty success")

	succeeded := wireToolResult(t, modelrepo.Message{
		Role: "tool", ToolCallID: "tu_1", Content: "a.txt",
	})
	assert.False(t, succeeded.IsError, "a successful call carries no is_error member")
	assert.Equal(t, "tu_1", succeeded.ToolUseID)
	assert.Equal(t, "a.txt", succeeded.Content, "a text-only result keeps the string form")
}

func TestUnit_Build_ToolResultCarriesImageBlocks(t *testing.T) {
	block := wireToolResult(t, modelrepo.Message{
		Role:       "tool",
		ToolCallID: "tu_1",
		Content:    "the screenshot",
		Images:     []modelrepo.ImagePart{{Data: []byte{0x89, 0x50, 0x4e, 0x47}, MimeType: "image/png"}},
	})

	blocks, ok := block.Content.([]any)
	require.True(t, ok, "a result with attachments takes the multi-block form, got %T", block.Content)
	require.Len(t, blocks, 2)

	text := blocks[0].(map[string]any)
	assert.Equal(t, "text", text["type"])
	assert.Equal(t, "the screenshot", text["text"])

	image := blocks[1].(map[string]any)
	assert.Equal(t, "image", image["type"])
	source := image["source"].(map[string]any)
	assert.Equal(t, "base64", source["type"])
	assert.Equal(t, "image/png", source["media_type"])
	assert.NotEmpty(t, source["data"])
}

func TestUnit_Build_ToolResultRejectsUnsupportedImageMediaType(t *testing.T) {
	_, _, err := msgcodec.Build([]modelrepo.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{tc("tu_1", "fs.list", `{}`)}},
		{Role: "tool", ToolCallID: "tu_1", Content: "shot", Images: []modelrepo.ImagePart{{Data: []byte{1}, MimeType: "image/tiff"}}},
	}, &modelrepo.ChatConfig{})

	require.ErrorContains(t, err, "image/tiff")
}
