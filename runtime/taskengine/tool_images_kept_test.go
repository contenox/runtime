package taskengine_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/llmrepo"
	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func shots(n int) []taskengine.ImagePart {
	out := make([]taskengine.ImagePart, n)
	for i := range out {
		out[i] = taskengine.ImagePart{Data: []byte{0x89, 'P', 'N', 'G', byte(i)}, MimeType: "image/png"}
	}
	return out
}

func screenshotHistory() taskengine.ChatHistory {
	call := func(id string) taskengine.Message {
		return taskengine.Message{Role: "assistant", CallTools: []taskengine.ToolCall{{
			ID: id, Type: "function",
			Function: taskengine.FunctionCall{Name: "local_fs.read_image", Arguments: `{"path":"shot.png"}`},
		}}}
	}
	return taskengine.ChatHistory{Messages: []taskengine.Message{
		{Role: "user", Content: "look at the page"},
		call("first"),
		{Role: "tool", ToolCallID: "first", Content: "first.png", Images: shots(2)},
		call("second"),
		{Role: "tool", ToolCallID: "second", Content: "second.png", Images: shots(2)},
		{Role: "user", Content: "and now?"},
	}}
}

func chatWithScreenshots(t *testing.T, opts ...taskengine.ExecOption) ([]libmodelprovider.Message, taskengine.ChatHistory) {
	t.Helper()
	var seen []libmodelprovider.Message
	model := &mockModelRepo{
		chatFunc: func(_ context.Context, _ llmrepo.Request, messages []libmodelprovider.Message, _ ...libmodelprovider.ChatArgument) (libmodelprovider.ChatResult, llmrepo.Meta, error) {
			seen = append([]libmodelprovider.Message(nil), messages...)
			return libmodelprovider.ChatResult{Message: libmodelprovider.Message{Role: "assistant", Content: "done"}}, llmrepo.Meta{ModelName: "test-model"}, nil
		},
	}
	exec, err := taskengine.NewExec(context.Background(), model, &imageToolsRepo{}, libtracker.NoopTracker{}, opts...)
	require.NoError(t, err)
	out, _, _, err := exec.TaskExec(
		context.Background(), time.Now().UTC(), 100000,
		&taskengine.ChainContext{},
		&taskengine.TaskDefinition{ID: "chat", Handler: taskengine.HandleChatCompletion, ExecuteConfig: &taskengine.LLMExecutionConfig{Model: "test-model"}},
		screenshotHistory(),
		taskengine.DataTypeChatHistory,
	)
	require.NoError(t, err)
	hist, ok := out.(taskengine.ChatHistory)
	require.True(t, ok, "got %T", out)
	return seen, hist
}

func toolMessage(t *testing.T, msgs []libmodelprovider.Message, id string) libmodelprovider.Message {
	t.Helper()
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == id {
			return m
		}
	}
	t.Fatalf("no tool message %q", id)
	return libmodelprovider.Message{}
}

func TestUnit_ChatCompletion_SendsEveryToolImageAgainUnlessTheOperatorOptsIn(t *testing.T) {
	seen, _ := chatWithScreenshots(t)
	require.Len(t, toolMessage(t, seen, "first").Images, 2)
	require.Len(t, toolMessage(t, seen, "second").Images, 2)

	seen, _ = chatWithScreenshots(t, taskengine.WithToolImagesKept(func(context.Context) int { return 0 }))
	require.Len(t, toolMessage(t, seen, "first").Images, 2)
}

func TestUnit_ChatCompletion_ToolImagesKeptSendsOnlyTheNewest(t *testing.T) {
	seen, hist := chatWithScreenshots(t, taskengine.WithToolImagesKept(func(context.Context) int { return 3 }))

	newest := toolMessage(t, seen, "second")
	require.Len(t, newest.Images, 2)
	require.Equal(t, "second.png", newest.Content)

	older := toolMessage(t, seen, "first")
	require.Len(t, older.Images, 1)
	require.Equal(t, "first.png\n(1 earlier image(s) from this result are no longer attached: only the newest 3 tool images are sent again)", older.Content)

	for _, m := range hist.Messages {
		if m.Role == "tool" {
			require.Len(t, m.Images, 2, "the stored history keeps every image; only the request is trimmed")
		}
	}
}
