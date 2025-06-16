package taskengine_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_TaskExec_PromptToString(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "mock-result",
		MockRawResponse: "mock-response",
		MockError:       nil,
	}

	task := &taskengine.ChainTask{
		Type: taskengine.RawString,
	}

	output, _, rawResp, err := mockExec.TaskExec(context.Background(), time.Now(), llmresolver.Randomly, task, "What is 2+2?", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "mock-result", output)
	require.Equal(t, "mock-response", rawResp)
}
