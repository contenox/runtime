package taskengine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/testingsetup"
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

	output, raw, err := exec.TaskExec(context.Background(), llmresolver.Randomly, task, "hello")
	require.NoError(t, err)
	require.Equal(t, "prompted response for: hello", output)
	require.Equal(t, "prompted response for: hello", raw)
}

func TestSimpleExec_TaskExec(t *testing.T) {
	// if os.Getenv("SMOKETESTS") == "" {
	// 	t.Skip("Set env SMOKETESTS to true to run this test")
	// }
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:1.5b",
	}
	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	defer cleanup()
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
		response, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
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
		)
		require.NoError(t, err)
		require.Equal(t, true, response)
		require.Equal(t, "true", formatted)
	})
	t.Run("PromptToNumber", func(t *testing.T) {
		response, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "number-test",
			Type: taskengine.PromptToNumber,
		}, "Respond with only the number 10 as a digit, no other text.")
		require.NoError(t, err)
		require.Equal(t, 10, response)
		require.Equal(t, "10", formatted)
	})
	t.Run("PromptToScore", func(t *testing.T) {
		response, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "score-test",
			Type: taskengine.PromptToScore,
		}, "Respond with exactly the number 7.5, no other text.")
		require.NoError(t, err)
		require.Equal(t, 7.5, response)
		require.Equal(t, "7.50", formatted)
	})

	t.Run("PromptToRange", func(t *testing.T) {
		response, formatted, err := exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "range-test",
			Type: taskengine.PromptToRange,
		}, "Echo the Input. Input: 3-5")
		require.NoError(t, err)
		require.Equal(t, "3-5", response)
		require.Equal(t, "3-5", formatted)

		// Test single number to range conversion
		response, formatted, err = exec.TaskExec(ctx, llmresolver.Randomly, &taskengine.ChainTask{
			ID:   "range-single-test",
			Type: taskengine.PromptToRange,
		}, "Respond with the number 4, no other text.")
		require.NoError(t, err)
		require.Equal(t, "4-4", response)
		require.Equal(t, "4-4", formatted)
	})
}
