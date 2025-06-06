package taskengine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/google/uuid"
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

	exec, err := taskengine.NewExec(context.Background(), mockRepo, taskengine.NewMockHookRegistry())
	require.NoError(t, err)

	task := &taskengine.ChainTask{
		Type: taskengine.PromptToString,
	}

	output, _, raw, err := exec.TaskExec(context.Background(), llmresolver.Randomly, task, "hello", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "prompted response for: hello", output)
	require.Equal(t, "prompted response for: hello", raw)
}

func TestSimpleExec_TaskExec(t *testing.T) {
	if os.Getenv("SMOKETESTS") == "" {
		t.Skip("Set env SMOKETESTS to true to run this test")
	}
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:1.5b",
	}

	ctx, state, dbInstance, cleanup, err := testingsetup.New(context.Background(), serverops.NoopTracker{}).
		WithTriggerChan().
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		WithModel("smollm2:135m").
		RunState().
		RunDownloadManager().
		WithDefaultUser().
		WaitForModel("smollm2:135m").
		Build()
	defer cleanup()
	require.NoError(t, err)
	execRepo, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	exec, err := taskengine.NewExec(ctx, execRepo, nil) // TODO:
	if err != nil {
		log.Fatalf("initializing the taskengine failed: %v", err)
	}

	require.Eventually(t, func() bool {
		currentState := state.Get(ctx)
		r, err := json.Marshal(currentState)
		if err != nil {
			t.Logf("error marshaling state: %v", err)
			return false
		}
		dst := &bytes.Buffer{}
		if err := json.Compact(dst, r); err != nil {
			t.Logf("error compacting JSON: %v", err)
			return false
		}
		return strings.Contains(string(r), `"name":"qwen2.5:1.5b"`)
	}, 2*time.Minute, 100*time.Millisecond)
	runtime := state.Get(ctx)
	backendID := ""
	foundExecModel := false
	for _, runtimeState := range runtime {
		backendID = runtimeState.Backend.ID
		for _, lmr := range runtimeState.PulledModels {
			if lmr.Model == "qwen2.5:1.5b" {
				foundExecModel = true
			}
		}
	}
	if !foundExecModel {
		t.Fatalf("qwen2.5:1.5b not found")
	}
	err = store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backendID)
	if err != nil {
		t.Fatalf("failed to assign backend to pool: %v", err)
	}
	// sanity-check
	backends, err := store.New(dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, serverops.EmbedPoolID)
	if err != nil {
		t.Fatalf("failed to list backends for pool: %v", err)
	}
	found2 := false
	for _, backend2 := range backends {
		found2 = backend2.ID == backendID
		if found2 {
			break
		}
	}
	if !found2 {
		t.Fatalf("backend not found in pool")
	}
	// sanity check
	provider, err := execRepo.GetProvider(ctx)
	require.NoError(t, err)
	require.True(t, provider.CanPrompt())
	require.GreaterOrEqual(t, len(provider.GetBackendIDs()), 1)
	prompt, err := provider.GetPromptConnection(provider.GetBackendIDs()[0])
	require.NoError(t, err)
	resp, err := prompt.Prompt(ctx, "say hi!")
	require.NoError(t, err)
	t.Log(t, resp)
	t.Run("simple test-case", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "simple",
			Type: taskengine.PromptToCondition,
			ConditionMapping: map[string]bool{
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
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "number-test",
			Type: taskengine.PromptToNumber,
		}, "Respond with only the number 10 as a digit, no other text.", taskengine.DataTypeInt)
		require.NoError(t, err)
		require.Equal(t, 10, response)
		require.Equal(t, "10", formatted)
	})
	t.Run("PromptToScore", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "score-test",
			Type: taskengine.PromptToScore,
		}, "Respond with exactly the number 7.5, no other text.", taskengine.DataTypeFloat)
		require.NoError(t, err)
		require.Equal(t, 7.5, response)
		require.Equal(t, "7.50", formatted)
	})

	t.Run("PromptToRange", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "range-test",
			Type: taskengine.PromptToRange,
		}, "Echo the Input. Input: 3-5", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "3-5", response)
		require.Equal(t, "3-5", formatted)

		// Test single number to range conversion
		response, _, formatted, err = exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "range-single-test",
			Type: taskengine.PromptToRange,
		}, "Respond with the number 4, no other text.", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "4-4", response)
		require.Equal(t, "4-4", formatted)
	})

	t.Run("ConditionCaseSensitive", func(t *testing.T) {
		_, _, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "condition-case-insensitive",
			Type: taskengine.PromptToCondition,
			ConditionMapping: map[string]bool{
				"yes": true,
				"no":  false,
			},
		}, "Respond with only the uppercase word 'YES'", taskengine.DataTypeString)
		require.Error(t, err)
	})

	t.Run("ConditionInvalidResponse", func(t *testing.T) {
		_, _, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "condition-invalid",
			Type: taskengine.PromptToCondition,
			ConditionMapping: map[string]bool{
				"yes": true,
				"no":  false,
			},
		}, "Respond with 'maybe'", taskengine.DataTypeString)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse into valid condition")
	})

	t.Run("RangeReverseNumbers", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "range-reverse-test",
			Type: taskengine.PromptToRange,
		}, "Echo the Input. Input: 5-3", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, "5-3", response)
		require.Equal(t, "5-3", formatted)
	})

	t.Run("ScoreIntegerValue", func(t *testing.T) {
		response, _, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "score-integer-test",
			Type: taskengine.PromptToScore,
		}, "Respond with exactly the number 7, no decimal places or other text.", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, 7.0, response)
		require.Equal(t, "7.00", formatted)
	})

	t.Run("NumberInvalidFloat", func(t *testing.T) {
		_, _, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "number-invalid-test",
			Type: taskengine.PromptToNumber,
		}, "Respond with '10.5'", taskengine.DataTypeString)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid syntax")
	})

	t.Run("HookTaskError", func(t *testing.T) {
		// Test with missing hook definition
		_, _, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "hook-missing-def",
			Type: taskengine.Hook,
		}, "", taskengine.DataTypeString)
		require.Error(t, err)
		require.Contains(t, err.Error(), "hook task missing hook definition")

		// Test with unimplemented hook
		_, _, _, err = exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "hook-unimplemented",
			Type: taskengine.Hook,
			Hook: &taskengine.HookCall{Type: "test-hook"},
		}, "", taskengine.DataTypeString)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unimplemented")
	})

	t.Run("PromptToStringEdgeCases", func(t *testing.T) {
		// Empty input
		output, _, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			Type: taskengine.PromptToString,
		}, "", taskengine.DataTypeString)
		require.Error(t, err)

		// Long input
		longInput := strings.Repeat("repeat this ", 10)
		output, _, _, err = exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			Type: taskengine.PromptToString,
		}, "Echo exactly this including the repetition: "+longInput, taskengine.DataTypeString)
		require.NoError(t, err)
		require.Contains(t, longInput, output)
	})

	t.Run("NumberWithSpaces", func(t *testing.T) {
		response, formatted, _, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "number-space-test",
			Type: taskengine.PromptToNumber,
		}, "Respond with ' 42 ' including spaces", taskengine.DataTypeString)
		require.NoError(t, err)
		require.Equal(t, 42, response)
		require.Equal(t, "42", formatted)
	})
}
