package taskengine_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestSimpleExec_TaskExec_PromptToString(t *testing.T) {
	// mockClient := &serverops.MockPromptExecClient{}
	mockProvider := &modelprovider.MockProvider{
		Name:          "mock-model",
		CanPromptFlag: true,
		ContextLength: 2048,
		ID:            uuid.NewString(),
		Backends:      []string{"my-backend-1"},
	}

	mockRepo := &llmrepo.MockModelRepo{
		Provider: mockProvider,
	}

	exec, err := taskengine.NewExec(context.Background(), mockRepo, nil)
	require.NoError(t, err)

	task := &taskengine.ChainTask{
		Type: taskengine.PromptToString,
	}

	output, raw, err := exec.TaskExec(context.Background(), task, "hello")
	require.NoError(t, err)
	require.Equal(t, "prompted response for: hello", output)
	require.Equal(t, "prompted response for: hello", raw)
}
