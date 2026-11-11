package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_Converse_ToolResultCarriesImageBlocks(t *testing.T) {
	m := converseModel{name: "anthropic.claude-sonnet-5-5"}
	messages := []modelrepo.Message{
		{Role: "user", Content: "look"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{{ID: "call-1", Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "local_fs.read_image", Arguments: `{"path":"shot.png"}`}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "shot.png: image/png", Images: []modelrepo.ImagePart{{MimeType: "image/png", Data: []byte{1, 2, 3}}}},
	}
	cfg := &modelrepo.ChatConfig{Tools: []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "local_fs.read_image", Parameters: map[string]any{"type": "object"}}}}}
	in, _, err := buildConverseInput(m, messages, cfg)
	require.NoError(t, err)

	var found bool
	for _, msg := range in.Messages {
		for _, block := range msg.Content {
			result, ok := block.(*types.ContentBlockMemberToolResult)
			if !ok {
				continue
			}
			found = true
			require.Len(t, result.Value.Content, 2)
			_, isText := result.Value.Content[0].(*types.ToolResultContentBlockMemberText)
			img, isImage := result.Value.Content[1].(*types.ToolResultContentBlockMemberImage)
			require.True(t, isText)
			require.True(t, isImage)
			require.Equal(t, types.ImageFormatPng, img.Value.Format)
		}
	}
	require.True(t, found)
}
