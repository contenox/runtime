package openai

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func toolImageMessages() []modelrepo.Message {
	return []modelrepo.Message{
		{Role: "user", Content: "look at the screenshot"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{{ID: "call-1", Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "local_fs.read_image", Arguments: `{"path":"shot.png"}`}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "shot.png: image/png, 1 KiB, 2x2", Images: []modelrepo.ImagePart{{MimeType: "image/png", Data: []byte{1, 2, 3}}}},
	}
}

func TestUnit_Responses_ToolResultImagesFollowAsUserInput(t *testing.T) {
	req, _, err := buildOpenAIResponsesRequestWithCapabilities("gpt-5.6", toolImageMessages(), nil, true)
	require.NoError(t, err)
	var sawOutput bool
	for i, item := range req.Input {
		if item.Type != "function_call_output" {
			continue
		}
		sawOutput = true
		require.Equal(t, "shot.png: image/png, 1 KiB, 2x2", *item.Output)
		require.Less(t, i+1, len(req.Input))
		follow := req.Input[i+1]
		require.Equal(t, "message", follow.Type)
		require.Equal(t, "user", follow.Role)
		parts, ok := follow.Content.([]openAIResponseInputContent)
		require.True(t, ok)
		require.Len(t, parts, 2)
		require.Equal(t, "input_text", parts[0].Type)
		require.Contains(t, parts[0].Text, "call-1")
		require.Equal(t, "input_image", parts[1].Type)
		require.Contains(t, parts[1].ImageURL, "data:image/png;base64,")
	}
	require.True(t, sawOutput)
}

func TestUnit_ChatCompletions_ToolResultImagesFollowAsUserMessage(t *testing.T) {
	req, _, err := buildOpenAIRequestForDialect(nil, "gpt-4.1", toolImageMessages(), nil, nil, false)
	require.NoError(t, err)
	var toolIndex = -1
	for i, m := range req.Messages {
		if m.Role == "tool" {
			toolIndex = i
		}
	}
	require.GreaterOrEqual(t, toolIndex, 0)
	require.Equal(t, "shot.png: image/png, 1 KiB, 2x2", req.Messages[toolIndex].Content)
	require.Less(t, toolIndex+1, len(req.Messages))
	follow := req.Messages[toolIndex+1]
	require.Equal(t, "user", follow.Role)
	parts, ok := follow.Content.([]apiContentPart)
	require.True(t, ok)
	require.Len(t, parts, 2)
	require.Equal(t, "image_url", parts[1].Type)
}

func TestUnit_ChatCompletions_ParallelToolImagesFollowTheWholeBatch(t *testing.T) {
	call := func(id, path string) modelrepo.ToolCall {
		return modelrepo.ToolCall{ID: id, Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "local_fs.read_image", Arguments: `{"path":"` + path + `"}`}}
	}
	messages := []modelrepo.Message{
		{Role: "user", Content: "compare the two screenshots"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{call("call-a", "a.png"), call("call-b", "b.png")}},
		{Role: "tool", ToolCallID: "call-a", Content: "a.png", Images: []modelrepo.ImagePart{{MimeType: "image/png", Data: []byte{1}}}},
		{Role: "tool", ToolCallID: "call-b", Content: "b.png", Images: []modelrepo.ImagePart{{MimeType: "image/png", Data: []byte{2}}}},
		{Role: "assistant", Content: "They differ."},
	}
	req, _, err := buildOpenAIRequestForDialect(nil, "gpt-4.1", messages, nil, nil, false)
	require.NoError(t, err)

	roles := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		roles = append(roles, m.Role)
	}
	require.Equal(t, []string{"user", "assistant", "tool", "tool", "user", "assistant"}, roles,
		"tool results must directly follow the assistant turn that called them")

	parts, ok := req.Messages[4].Content.([]apiContentPart)
	require.True(t, ok)
	var labels, images int
	for _, p := range parts {
		switch p.Type {
		case "text":
			labels++
		case "image_url":
			images++
		}
	}
	require.Equal(t, 2, labels, "each image says which call returned it")
	require.Equal(t, 2, images)
	require.Contains(t, parts[0].Text, "call-a")
	require.Contains(t, parts[2].Text, "call-b")
}
