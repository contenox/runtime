package taskengine_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

type imageToolsRepo struct{ images int }

func (r *imageToolsRepo) Exec(_ context.Context, _ time.Time, _ any, _ bool, _ *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	out := &taskengine.ToolImageResult{Text: "screenshot.png: image/png, 1 KiB, 2x2"}
	for i := 0; i < r.images; i++ {
		out.Images = append(out.Images, taskengine.ImagePart{Data: []byte{0x89, 'P', 'N', 'G', byte(i)}, MimeType: "image/png"})
	}
	return out, taskengine.DataTypeString, nil
}

func (r *imageToolsRepo) Supports(context.Context) ([]string, error) {
	return []string{"local_fs"}, nil
}

func (r *imageToolsRepo) GetSchemasForSupportedTools(context.Context) (map[string]*openapi3.T, error) {
	return map[string]*openapi3.T{}, nil
}

func (r *imageToolsRepo) GetToolsForToolsByName(_ context.Context, name string) ([]taskengine.Tool, error) {
	if name == "local_fs" {
		return []taskengine.Tool{{Type: "function", Function: taskengine.FunctionTool{Name: "read_image"}}}, nil
	}
	return nil, taskengine.ErrToolsNotFound
}

func runImageTool(t *testing.T, images int) taskengine.Message {
	t.Helper()
	exec, err := taskengine.NewExec(context.Background(), &mockModelRepo{}, &imageToolsRepo{images: images}, libtracker.NoopTracker{})
	require.NoError(t, err)
	chainCtx := &taskengine.ChainContext{Tools: map[string]taskengine.ToolWithResolution{
		"local_fs.read_image": {
			Tool:      taskengine.Tool{Type: "function", Function: taskengine.FunctionTool{Name: "local_fs.read_image"}},
			ToolsName: "local_fs",
		},
	}}
	history := taskengine.ChatHistory{Messages: []taskengine.Message{{
		Role: "assistant",
		CallTools: []taskengine.ToolCall{{
			ID:       "call-img",
			Type:     "function",
			Function: taskengine.FunctionCall{Name: "local_fs.read_image", Arguments: `{"path":"screenshot.png"}`},
		}},
	}}}
	out, dt, _, err := exec.TaskExec(
		context.Background(), time.Now().UTC(), 4000,
		chainCtx,
		&taskengine.TaskDefinition{ID: "exec", Handler: taskengine.HandleExecuteToolCalls, ExecuteConfig: &taskengine.LLMExecutionConfig{Tools: []string{"local_fs"}}},
		history,
		taskengine.DataTypeChatHistory,
	)
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeChatHistory, dt)
	hist, ok := out.(taskengine.ChatHistory)
	require.True(t, ok)
	return hist.Messages[len(hist.Messages)-1]
}

func TestUnit_ExecuteToolCalls_ImageResultLandsOnTheToolMessage(t *testing.T) {
	last := runImageTool(t, 1)
	require.Equal(t, "tool", last.Role)
	require.Equal(t, "call-img", last.ToolCallID)
	require.Equal(t, "screenshot.png: image/png, 1 KiB, 2x2", last.Content)
	require.Len(t, last.Images, 1)
	require.Equal(t, "image/png", last.Images[0].MimeType)
}

func TestUnit_ExecuteToolCalls_ImageResultIsBounded(t *testing.T) {
	last := runImageTool(t, 6)
	require.Len(t, last.Images, 4)
	require.Contains(t, last.Content, "2 image(s) were not attached")
}

func TestUnit_ToolsTask_ImageResultBecomesTextWithANote(t *testing.T) {
	exec, err := taskengine.NewExec(context.Background(), &mockModelRepo{}, &imageToolsRepo{images: 2}, libtracker.NoopTracker{})
	require.NoError(t, err)
	out, dt, _, err := exec.TaskExec(
		context.Background(), time.Now().UTC(), 4000,
		&taskengine.ChainContext{},
		&taskengine.TaskDefinition{ID: "shot", Handler: taskengine.HandleTools, Tools: &taskengine.ToolsCall{Name: "local_fs", ToolName: "read_image"}},
		"screenshot.png",
		taskengine.DataTypeString,
	)
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, dt)
	text, ok := out.(string)
	require.True(t, ok, "a tools task hands on text, got %T", out)
	require.Equal(t, "screenshot.png: image/png, 1 KiB, 2x2\n(2 image(s) were not attached: a tools task has no model to show them to)", text)
}
