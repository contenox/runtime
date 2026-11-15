package taskengine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type contextMeterRepo struct{ mockModelRepo }

func (r *contextMeterRepo) CountTokens(ctx context.Context, model, content string) (int, error) {
	return len(content), nil
}

func TestContextShiftAtEffectiveWindow(t *testing.T) {
	sink := &captureTaskEventSink{}
	ctx := taskengine.WithTaskEventSink(context.Background(), sink)
	ctx = taskengine.WithRequestedContextLength(ctx, 1000000)
	repo := &contextMeterRepo{}
	var sent []modelrepo.Message
	repo.chatFunc = func(_ context.Context, req llmrepo.Request, messages []modelrepo.Message, _ ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
		require.Equal(t, 1000000, req.ContextLength)
		sent = messages
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: "done"}, Usage: &modelrepo.TokenUsage{TotalTokens: 12345}}, llmrepo.Meta{}, nil
	}
	exec, err := taskengine.NewExec(ctx, repo, &gatedToolsRepo{}, libtracker.NoopTracker{})
	require.NoError(t, err)
	task := taskengine.TaskDefinition{ID: "chat", Handler: taskengine.HandleChatCompletion, ExecuteConfig: &taskengine.LLMExecutionConfig{Model: "million", Shift: true}}
	history := taskengine.ChatHistory{Messages: []taskengine.Message{{Role: "user", Content: strings.Repeat("x", 500000)}, {Role: "assistant", Content: "understood"}, {Role: "user", Content: "latest"}}}
	for _, limit := range []int{1000000, 131072, 1000000} {
		sink.events = nil
		_, _, _, err = exec.TaskExec(ctx, time.Now(), limit, &taskengine.ChainContext{}, &task, history, taskengine.DataTypeChatHistory)
		require.NoError(t, err)
		var shifts []*taskengine.ContextShift
		for _, ev := range sink.events {
			if ev.ContextShift != nil {
				shifts = append(shifts, ev.ContextShift)
			}
		}
		if limit == 1000000 {
			require.Empty(t, shifts)
			require.Equal(t, history.Messages[0].Content, sent[0].Content)
		} else {
			require.Len(t, shifts, 1)
			require.Equal(t, 1000000, shifts[0].Requested)
			require.Equal(t, limit, shifts[0].Window)
			require.Equal(t, 1, shifts[0].Removed)
			require.Equal(t, 2, shifts[0].Kept)
			require.Equal(t, "understood", sent[0].Content)
		}
		last := sink.events[len(sink.events)-1]
		require.Equal(t, 12345, last.TokenUsed)
		require.Equal(t, limit, last.TokenSize)
	}
	task.ExecuteConfig.MaxTokens = new(16384)
	history.Messages[0].Content = strings.Repeat("x", 1000000-16384-len("understoodlatest"))
	sink.events = nil
	_, _, _, err = exec.TaskExec(ctx, time.Now(), 1000000, &taskengine.ChainContext{}, &task, history, taskengine.DataTypeChatHistory)
	require.NoError(t, err)
	require.Len(t, sent, 3)
	for _, event := range sink.events {
		require.Nil(t, event.ContextShift)
	}
	history.Messages[0].Content += "x"
	_, _, _, err = exec.TaskExec(ctx, time.Now(), 1000000, &taskengine.ChainContext{}, &task, history, taskengine.DataTypeChatHistory)
	require.NoError(t, err)
	require.Len(t, sent, 2)
	history.Messages = []taskengine.Message{{Role: "system", Content: "keep this"}, {Role: "user", Content: strings.Repeat("x", 1000000)}}
	sent = nil
	_, _, _, err = exec.TaskExec(ctx, time.Now(), 1000000, &taskengine.ChainContext{}, &task, history, taskengine.DataTypeChatHistory)
	require.ErrorIs(t, err, taskengine.ErrContextLengthExceeded)
	require.Nil(t, sent)
}
