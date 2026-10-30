package taskengine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

type fixedResultToolsRepo struct {
	result string
	err    error
}

func (r *fixedResultToolsRepo) Exec(context.Context, time.Time, any, bool, *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if r.err != nil {
		return nil, taskengine.DataTypeAny, r.err
	}
	return r.result, taskengine.DataTypeString, nil
}

func (r *fixedResultToolsRepo) Supports(context.Context) ([]string, error) {
	return []string{"local_fs"}, nil
}

func (r *fixedResultToolsRepo) GetSchemasForSupportedTools(context.Context) (map[string]*openapi3.T, error) {
	return map[string]*openapi3.T{}, nil
}

func (r *fixedResultToolsRepo) GetToolsForToolsByName(context.Context, string) ([]taskengine.Tool, error) {
	return []taskengine.Tool{{Type: "function", Function: taskengine.FunctionTool{Name: "tool"}}}, nil
}

// runThirdIdenticalCall replays two earlier identical calls (with the given
// stored results) and executes the third one.
func runThirdIdenticalCall(t *testing.T, repo *fixedResultToolsRepo, priorFailed bool, priorContent [2]string, ctxLength int) string {
	t.Helper()
	exec, err := taskengine.NewExec(context.Background(), &mockModelRepo{}, repo, libtracker.NoopTracker{})
	require.NoError(t, err)

	chainCtx := &taskengine.ChainContext{Tools: map[string]taskengine.ToolWithResolution{
		"local_fs.read_file": {
			Tool:      taskengine.Tool{Type: "function", Function: taskengine.FunctionTool{Name: "local_fs.read_file"}},
			ToolsName: "local_fs",
		},
	}}
	call := func(id string) taskengine.ToolCall {
		return taskengine.ToolCall{ID: id, Type: "function", Function: taskengine.FunctionCall{Name: "local_fs.read_file", Arguments: `{"path":"a"}`}}
	}
	history := taskengine.ChatHistory{Messages: []taskengine.Message{
		{Role: "assistant", CallTools: []taskengine.ToolCall{call("c1")}},
		{Role: "tool", ToolCallID: "c1", Content: priorContent[0], ToolResultError: priorFailed},
		{Role: "assistant", CallTools: []taskengine.ToolCall{call("c2")}},
		{Role: "tool", ToolCallID: "c2", Content: priorContent[1], ToolResultError: priorFailed},
		{Role: "assistant", CallTools: []taskengine.ToolCall{call("c3")}},
	}}

	out, _, _, err := exec.TaskExec(context.Background(), time.Now().UTC(), ctxLength, chainCtx,
		&taskengine.TaskDefinition{ID: "exec", Handler: taskengine.HandleExecuteToolCalls, ExecuteConfig: &taskengine.LLMExecutionConfig{Tools: []string{"local_fs"}}},
		history, taskengine.DataTypeChatHistory)
	require.NoError(t, err)
	hist := out.(taskengine.ChatHistory)
	last := hist.Messages[len(hist.Messages)-1]
	require.Equal(t, "c3", last.ToolCallID)
	return last.Content
}

func TestUnit_RepeatGuidance_SurvivesTheOutputCap(t *testing.T) {
	repo := &fixedResultToolsRepo{result: strings.Repeat("x", 5000)}
	content := runThirdIdenticalCall(t, repo, false, [2]string{"ok", "ok"}, 520)

	require.Contains(t, content, "tool_result_too_large", "the result must have been capped")
	require.Contains(t, content, "[harness] This exact local_fs.read_file call has already succeeded 3 times",
		"guidance is added after capping, so the cap cannot cut it off")
}

func TestUnit_RepeatGuidance_SameErrorWordingOnlyWhenErrorsMatch(t *testing.T) {
	repo := &fixedResultToolsRepo{err: errors.New("boom")}
	const stored = `"tool local_fs.read_file execution failed: boom"`

	same := runThirdIdenticalCall(t, repo, true, [2]string{stored, stored}, 4000)
	require.Contains(t, same, "failed 3 times with the same error")

	different := runThirdIdenticalCall(t, repo, true, [2]string{`"tool local_fs.read_file execution failed: other"`, stored}, 4000)
	require.Contains(t, different, "attempted 3 times and keeps failing")
	require.NotContains(t, different, "with the same error")
}
