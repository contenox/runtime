package llmrepo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/stretchr/testify/require"
)

func setupTestEnvironment(t *testing.T) (context.Context, *serverops.Config, libdb.DBManager, *runtimestate.State, func()) {
	ctx := context.Background()
	config := &serverops.Config{
		EmbedModel:          "all-minilm:33m",
		JWTExpiry:           "1h",
		WorkerUserEmail:     "worker@internal",
		WorkerUserPassword:  "securepassword",
		WorkerUserAccountID: uuid.NewString(),
		SigningKey:          "test-signing-key",
	}

	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	return ctx, config, dbInstance, state, cleanup
}

func TestNew_InitializesPoolAndModel(t *testing.T) {

	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test initialization
	embedder, err := llmrepo.NewEmbedder(ctx, config, dbInstance, state)
	require.NoError(t, err)

	// Verify pool creation
	poolStore := store.New(dbInstance.WithoutTransaction())
	pool, err := poolStore.GetPool(ctx, serverops.EmbedPoolID)
	require.NoError(t, err)
	require.Equal(t, serverops.EmbedPoolName, pool.Name)
	require.Equal(t, "Internal Embeddings", pool.PurposeType)

	// Verify model creation
	modelStore := store.New(dbInstance.WithoutTransaction())
	tenantID, _ := uuid.Parse(serverops.TenantID)
	expectedModelID := uuid.NewSHA1(tenantID, []byte(config.EmbedModel)).String()

	model, err := modelStore.GetModel(ctx, expectedModelID)
	require.NoError(t, err)
	require.Equal(t, config.EmbedModel, model.Model)

	// Verify model assignment
	models, err := modelStore.ListModelsForPool(ctx, serverops.EmbedPoolID)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, expectedModelID, models[0].ID)

	// extract a valid backend
	backend := &store.Backend{}
	for _, l := range state.Get(ctx) {
		backend = &l.Backend
	}
	// assign backend to pool
	require.NoError(t, store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backend.ID))
	// wait for the model to download
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
		return strings.Contains(string(r), `"name":"all-minilm:33m"`)
	}, 1*time.Minute, 100*time.Millisecond)
	// reuse state for other testcases.
	t.Run("test get runtime", func(t *testing.T) {
		runtimeAdapter := embedder.GetRuntime(ctx)
		require.NotNil(t, runtimeAdapter)
		providers, err := runtimeAdapter(ctx, "Ollama")
		require.NoError(t, err)
		require.NotEmpty(t, providers)
	})
	t.Run("test basic embed call", func(t *testing.T) {
		provider, err := embedder.GetProvider(ctx)
		require.NoError(t, err)

		embedClient, err := provider.GetEmbedConnection(backend.BaseURL)
		require.NoError(t, err)

		// Test embedding
		embeddings, err := embedClient.Embed(context.Background(), "test text")
		require.NoError(t, err)
		require.NotEmpty(t, embeddings)
	})
	t.Run("test prompt execution", func(t *testing.T) {
		// Configure task model and initialize engine
		config.TasksModel = "smollm2:135m"
		taskEngine, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			currentState := state.Get(ctx)
			r, _ := json.Marshal(currentState)
			return strings.Contains(string(r), `"name":"smollm2:135m"`)
		}, 1*time.Minute, 100*time.Millisecond)

		// Get provider and test prompting
		provider, err := taskEngine.GetProvider(ctx)
		require.NoError(t, err)

		promptClient, err := provider.GetPromptConnection(backend.BaseURL)
		require.NoError(t, err)

		response, err := promptClient.Prompt(context.Background(), "What is 1+1?")
		require.NoError(t, err)
		require.Contains(t, response, "2")
	})
	t.Run("execute complex prompts", func(t *testing.T) {
		// Configure task model and initialize engine
		config.TasksModel = "qwen2.5:0.5b"

		taskEngine, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			currentState := state.Get(ctx)
			r, _ := json.Marshal(currentState)
			return strings.Contains(string(r), `"name":"qwen2.5:0.5b"`)
		}, 1*time.Minute, 100*time.Millisecond)
		provider, err := taskEngine.GetProvider(ctx)
		require.NoError(t, err)

		execClient, err := provider.GetPromptConnection(backend.BaseURL)
		require.NoError(t, err)

		text := `
		The concept of "flow state," often described as being "in the zone," is characterized by complete immersion and energized focus in an activity. Individuals experiencing flow often report a feeling of spontaneous joy and a deep sense of satisfaction. Achieving this state typically requires a clear set of goals, immediate feedback, and a balance between the perceived challenge of the task and one's perceived skills. While often associated with creative arts or sports, flow can be experienced in almost any activity that meets these conditions. Understanding and cultivating flow can lead to increased productivity and greater personal fulfillment.
		The city's public transportation system of Smalltown has seen significant improvements over the past decade, with new tram lines and more frequent bus services. Commuter satisfaction is reportedly at an all-time high. However, despite these advancements, traffic congestion in the downtown core during peak hours remains a major challenge. Several urban planners suggest that a more radical approach, such as congestion pricing or significantly expanding pedestrian-only zones, might be necessary to alleviate the gridlock. The mayor Jake Thompson, on the other hand, believes that further optimizing the existing public transit routes will eventually solve the problem.
		`
		prompt := fmt.Sprintf(`
Extract atomic semantic keywords like names, cities, dates and others from the following text. Avoid grouping or phrases. Return only important individual concepts, people, places, or terms. Format as a comma-separated list.

Text: %s`, text)
		response1, err := execClient.Prompt(context.Background(), prompt)
		require.NoError(t, err)
		t.Logf("Response 1: %s", response1)

		response2, err := execClient.Prompt(ctx, prompt)
		require.NoError(t, err)
		t.Logf("Response 2: %s", response2)
		prompt = fmt.Sprintf(`
For each keyword and type below, write a one-line description explaining what it refers to in the original text.

Keywords and types:
%s

Text: %s`, response2, text)
		response3, err := execClient.Prompt(ctx, prompt)
		response2 = strings.ToLower(response2)
		response1 = strings.ToLower(response1)
		response3 = strings.ToLower(response3)
		require.Contains(t, response1, "flow state")
		require.Contains(t, response1, "smalltown")
		require.Contains(t, response1, "jake thompson")

		require.Contains(t, response2, "flow state")
		require.Contains(t, response2, "smalltown")
	})
}

func TestGetProvider_NoBackends(t *testing.T) {
	if os.Getenv("SMOKETESTS") == "" {
		t.Skip("Set env SMOKETESTS to true to run this test")
	}
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	embedder, err := llmrepo.NewEmbedder(ctx, config, dbInstance, state)
	require.NoError(t, err)

	_, err = embedder.GetProvider(ctx)
	require.Error(t, err)
	require.EqualError(t, err, "no backends found")
}
