package taskengine_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/turninput"
	"github.com/stretchr/testify/require"
)

type steeringTools struct {
	imageToolsRepo
	onCall func()
}

func (s *steeringTools) Exec(ctx context.Context, start time.Time, input any, debug bool, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	s.onCall()
	return s.imageToolsRepo.Exec(ctx, start, input, debug, call)
}

func TestSteeringDuringToolExecutionReachesNextModelRequest(t *testing.T) {
	var queued []turninput.Message
	img := turninput.ImagePart{Data: []byte("png"), MimeType: "image/png"}
	clip := turninput.AudioPart{Data: []byte("wav"), MimeType: "audio/wav"}
	tools := &steeringTools{onCall: func() {
		queued = []turninput.Message{{ID: "correction-1", Text: "Focus on Windows", Images: []turninput.ImagePart{img}}, {ID: "correction-2", Audio: []turninput.AudioPart{clip}}}
	}}
	chain := &taskengine.ChainContext{Tools: map[string]taskengine.ToolWithResolution{
		"local_fs.read_image": {Tool: taskengine.Tool{Type: "function", Function: taskengine.FunctionTool{Name: "local_fs.read_image"}}, ToolsName: "local_fs"},
	}}
	ctx := taskengine.WithTaskEventScope(context.Background(), taskengine.TaskEventScope{ChainID: "chat"})
	ctx = taskengine.WithSteering(ctx, "chat", func() ([]turninput.Message, func()) {
		return queued, func() { queued = nil }
	})
	var seen []modelrepo.Message
	model := &mockModelRepo{chatFunc: func(_ context.Context, _ llmrepo.Request, messages []modelrepo.Message, _ ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
		seen = messages
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: "done"}}, llmrepo.Meta{ModelName: "test-model"}, nil
	}}
	exec, err := taskengine.NewExec(ctx, model, tools, libtracker.NoopTracker{})
	require.NoError(t, err)
	task := &taskengine.TaskDefinition{ID: "chat", Handler: taskengine.HandleChatCompletion, ExecuteConfig: &taskengine.LLMExecutionConfig{Model: "test-model", Tools: []string{"local_fs"}}}
	input := screenshotHistory()
	input.Messages = input.Messages[:2] // One unanswered tool call.
	out, _, _, err := exec.TaskExec(ctx, time.Now(), 10000, chain, task, input, taskengine.DataTypeChatHistory)
	require.NoError(t, err)
	require.Empty(t, queued)
	require.GreaterOrEqual(t, len(seen), 3)
	tail := seen[len(seen)-3:]
	require.Equal(t, "tool", tail[0].Role)
	require.Equal(t, "first", tail[0].ToolCallID)
	require.Equal(t, "user", tail[1].Role)
	require.Equal(t, "Focus on Windows", tail[1].Content)
	require.Equal(t, []modelrepo.ImagePart{{MimeType: img.MimeType, Data: img.Data}}, tail[1].Images)
	require.Empty(t, tail[2].Content)
	require.Equal(t, []modelrepo.AudioPart{{MimeType: clip.MimeType, Data: clip.Data}}, tail[2].Audio)
	history := out.(taskengine.ChatHistory).Messages
	require.Equal(t, "correction-1", history[len(history)-3].ID)
	require.Equal(t, "correction-2", history[len(history)-2].ID)
	require.Equal(t, []taskengine.ImagePart{img}, history[len(history)-3].Images)
	require.Equal(t, []taskengine.AudioPart{clip}, history[len(history)-2].Audio)
}
