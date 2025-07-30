package services_test

import (
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/contenox/activitytracker"
	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/testingsetup"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libmodelprovider"
	"github.com/contenox/runtime-mvp/libs/libmodelprovider/llmresolver"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnit_SimpleExec_TaskExec_PromptToString(t *testing.T) {
	mockProvider := &libmodelprovider.MockProvider{
		Name:          "mock-model",
		CanPromptFlag: true,
		ContextLength: 2048,
		ID:            uuid.NewString(),
		Backends:      []string{"my-backend-1"},
	}

	mockRepo := &llmrepo.MockModelRepo{
		Provider: mockProvider,
	}

	exec, err := taskengine.NewExec(context.Background(), mockRepo, hooks.NewMockHookRegistry(), &activitytracker.LogActivityTracker{})
	require.NoError(t, err)

	task := &taskengine.ChainTask{
		Type: taskengine.RawString,
	}

	output, _, _, err := exec.TaskExec(context.Background(), time.Now().UTC(), llmresolver.Randomly, 100, task, "hello", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "hello", output)
}

func TestSystem_SimpleExec_TaskExecSystemTest(t *testing.T) {
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:1.5b",
	}

	testenv := testingsetup.New(context.Background(), activitytracker.NoopTracker{}).
		WithTriggerChan().
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		RunState().
		RunDownloadManager().
		WithDefaultUser().
		Build()
	defer testenv.Cleanup()
	require.NoError(t, testenv.Err)
	ctx := testenv.Ctx
	execRepo, err := testenv.NewExecRepo(config)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	exec, err := taskengine.NewExec(ctx, execRepo, &hooks.MockHookRepo{}, &activitytracker.LogActivityTracker{})
	if err != nil {
		log.Fatalf("initializing the taskengine failed: %v", err)
	}
	require.NoError(t, testenv.AssignBackends(serverops.EmbedPoolID).Err)
	require.NoError(t, testenv.WaitForModel(config.TasksModel).Err)

	provider, err := execRepo.GetDefaultSystemProvider(ctx)
	require.NoError(t, err)
	require.True(t, provider.CanPrompt())
	require.GreaterOrEqual(t, len(provider.GetBackendIDs()), 1)
	prompt, err := provider.GetPromptConnection(ctx, provider.GetBackendIDs()[0])
	require.NoError(t, err)
	resp, err := prompt.Prompt(ctx, "say hi!")
	require.NoError(t, err)
	t.Log(t, resp)
	t.Run("simple test-case", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "simple",
			Type: taskengine.ConditionKey,
			ValidConditions: map[string]bool{
				"yes": true,
				"Yes": true,
				"no":  false,
				"No":  false,
			},
		},
			"respond with just 'yes'",
			taskengine.DataTypeString,
		)
		require.NoError(t, err)
		require.Equal(t, true, response)
		require.Equal(t, "true", formatted)
	})
	t.Run("PromptToNumber", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "number-test",
			Type: taskengine.ParseNumber,
		}, "Respond with only the number 10 as a digit, no other text.", taskengine.DataTypeInt)
		require.NoError(t, err)
		require.Equal(t, 10, response)
		require.Equal(t, "10", formatted)
	})
	t.Run("PromptToScore", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "score-test",
			Type: taskengine.ParseScore,
		}, "Respond with exactly the number 7.5, no other text.", taskengine.DataTypeFloat)
		require.NoError(t, err)
		require.Equal(t, 7.5, response)
		require.Equal(t, "7.50", formatted)
	})

	t.Run("PromptToRange", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "range-test",
			Type: taskengine.ParseRange,
		}, "Echo the Input. Input: 3-5", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "3-5", response)
		require.Equal(t, "3-5", formatted)

		// Test single number to range conversion
		response, _, formatted, err = exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "range-single-test",
			Type: taskengine.ParseRange,
		}, "Respond with the number 4, no other text.", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "4-4", response)
		require.Equal(t, "4-4", formatted)
	})

	t.Run("ConditionCaseSensitive", func(t *testing.T) {
		_, _, _, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "condition-case-insensitive",
			Type: taskengine.ConditionKey,
			ValidConditions: map[string]bool{
				"yes": true,
				"no":  false,
			},
		}, "Respond with only the uppercase word 'YES'", taskengine.DataTypeString)
		require.Error(t, err)
	})

	t.Run("ConditionInvalidResponse", func(t *testing.T) {
		_, _, res, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "condition-invalid",
			Type: taskengine.ConditionKey,
			ValidConditions: map[string]bool{
				"yes": true,
				"no":  false,
			},
		}, "Echo the input. Input: 'maybe'", taskengine.DataTypeBool)
		t.Log(res)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse into valid condition")
	})

	t.Run("RangeReverseNumbers", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "range-reverse-test",
			Type: taskengine.ParseRange,
		}, "Echo the Input as is. Input: 5-3", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "5-3", response)
		require.Equal(t, "5-3", formatted)
	})

	t.Run("ScoreIntegerValue", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "score-integer-test",
			Type: taskengine.ParseScore,
		}, "Respond with exactly the number 7, no decimal places or other text.", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, 7.0, response)
		require.Equal(t, "7.00", formatted)
	})

	t.Run("NumberInvalidFloat", func(t *testing.T) {
		_, _, _, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "number-invalid-test",
			Type: taskengine.ParseNumber,
		}, "Respond with '10.5'", taskengine.DataTypeString)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid syntax")
	})

	t.Run("HookTaskError", func(t *testing.T) {
		// Test with mock hook
		_, _, _, err = exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "hooks-implemented",
			Type: taskengine.Hook,
			Hook: &taskengine.HookCall{Type: "test-hook"},
		}, "", taskengine.DataTypeString)
		require.NoError(t, err)
	})

	t.Run("HookMockTaskNoError", func(t *testing.T) {
		// Test with mock hook
		_, _, _, err = exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "hooks-implemented",
			Type: taskengine.Hook,
			Hook: &taskengine.HookCall{Type: "test-hook"},
		}, "", taskengine.DataTypeString)
		require.NoError(t, err)
	})

	t.Run("PromptToStringEdgeCases", func(t *testing.T) {
		// Empty input
		_, _, _, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			Type: taskengine.RawString,
		}, "", taskengine.DataTypeString)
		require.Error(t, err)

		// Long input
		longInput := strings.Repeat("repeat this ", 10)
		output, _, _, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			Type: taskengine.RawString,
		}, "Echo exactly this including the repetition: "+longInput, taskengine.DataTypeString)
		require.NoError(t, err)
		require.Contains(t, longInput, output)
	})

	t.Run("NumberWithSpaces", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, time.Now().UTC(), llmresolver.Randomly, 100, &taskengine.ChainTask{
			ID:   "number-space-test",
			Type: taskengine.ParseNumber,
		}, "Respond with ' 42 '", taskengine.DataTypeInt)
		require.NoError(t, err)
		require.Equal(t, 42, response)
		require.Equal(t, "42", formatted)
	})
}
