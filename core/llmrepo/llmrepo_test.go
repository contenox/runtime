package llmrepo_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/testingsetup"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func setupTestEnvironment() (*serverops.Config, *testingsetup.Environment) {
	config := &serverops.Config{
		EmbedModel:          "granite-embedding:30m",
		JWTExpiry:           "1h",
		WorkerUserEmail:     "worker@internal",
		WorkerUserPassword:  "securepassword",
		WorkerUserAccountID: uuid.NewString(),
		SigningKey:          "test-signing-key",
	}

	return config, testingsetup.New(context.Background(), serverops.NoopTracker{}).
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
}

func TestSystem_EmbedAndPromptPipeline(t *testing.T) {
	config, env := setupTestEnvironment()
	if env.Err != nil {
		t.Fatal(env.Err)
	}
	defer env.Cleanup()

	// Test initialization
	embedder, err := env.NewEmbedder(config)
	require.NoError(t, err)
	ctx := env.Ctx
	// Verify pool creation
	storeInstance := env.Store()
	pool, err := storeInstance.GetPool(ctx, serverops.EmbedPoolID)
	require.NoError(t, err)
	require.Equal(t, serverops.EmbedPoolName, pool.Name)
	require.Equal(t, "Internal Embeddings", pool.PurposeType)

	tenantID, _ := uuid.Parse(serverops.TenantID)
	expectedModelID := uuid.NewSHA1(tenantID, []byte(config.EmbedModel)).String()

	model, err := storeInstance.GetModel(ctx, expectedModelID)
	require.NoError(t, err)
	require.Equal(t, config.EmbedModel, model.Model)

	// Verify model assignment
	models, err := storeInstance.ListModelsForPool(ctx, serverops.EmbedPoolID)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, expectedModelID, models[0].ID)

	// assign backend to pool
	require.NoError(t, env.AssignBackends(serverops.EmbedPoolID).Err)
	// wait for the model to download
	require.NoError(t, env.WaitForModel(config.EmbedModel).Err)
	// reuse state for other testcases.
	t.Run("test get runtime", func(t *testing.T) {
		runtimeAdapter := embedder.GetRuntime(ctx)
		require.NotNil(t, runtimeAdapter)
		providers, err := runtimeAdapter(ctx, "ollama")
		require.NoError(t, err)
		require.NotEmpty(t, providers)
	})
	t.Run("test basic embed call", func(t *testing.T) {
		provider, err := embedder.GetProvider(ctx)
		require.NoError(t, err)

		embedClient, err := env.GetEmbedConnection(provider)
		require.NoError(t, err)

		// Test embedding
		embeddings, err := embedClient.Embed(context.Background(), "test text")
		require.NoError(t, err)
		require.NotEmpty(t, embeddings)
	})

	t.Run("test prompt execution", func(t *testing.T) {
		// Configure task model and initialize engine
		config.TasksModel = "smollm2:135m"
		taskEngine, err := env.NewExecRepo(config)
		require.NoError(t, err)
		require.NoError(t, env.WaitForModel(config.TasksModel).Err)

		// Get provider and test prompting
		provider, err := taskEngine.GetProvider(ctx)
		require.NoError(t, err)

		promptClient, err := env.GetPromptConnection(provider)
		require.NoError(t, err)

		response, err := promptClient.Prompt(context.Background(), "What is 1+1?")
		require.NoError(t, err)
		require.Contains(t, response, "2")
	})

	t.Run("execute complex prompts", func(t *testing.T) {
		// Configure task model and initialize engine
		config.TasksModel = "qwen2.5:0.5b"

		taskEngine, err := env.NewExecRepo(config)
		require.NoError(t, err)
		require.NoError(t, env.WaitForModel(config.TasksModel).Err)

		// Get provider and test prompting
		provider, err := taskEngine.GetProvider(ctx)
		require.NoError(t, err)

		execClient, err := env.GetPromptConnection(provider)
		require.NoError(t, err)
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
		require.NoError(t, err)
		t.Logf("Response 3: %s", response3)
		response2 = strings.ToLower(response2)
		response1 = strings.ToLower(response1)
		response3 = strings.ToLower(response3)
		require.Contains(t, response1, "flow state")
		require.Contains(t, response1, "smalltown")
		require.Contains(t, response1, "jake thompson")

		require.Contains(t, response2, "flow state")
		require.Contains(t, response2, "smalltown")
		require.Contains(t, response3, "flow state")
	})
}
