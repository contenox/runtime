package llmembed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/testingsetup"
	"github.com/js402/cate/libs/libdb"
	"github.com/stretchr/testify/require"
)

func setupTestEnvironment(t *testing.T) (context.Context, *serverops.Config, libdb.DBManager, *runtimestate.State, func()) {
	ctx := context.Background()
	config := &serverops.Config{
		EmbedModel: "all-minilm:33m",
		JWTExpiry:  "1h",
	}

	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	return ctx, config, dbInstance, state, cleanup
}

func TestNew_InitializesPoolAndModel(t *testing.T) {
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test initialization
	_, err := llmembed.New(ctx, config, dbInstance, state)
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
}

func TestGetProvider_WithBackends(t *testing.T) {
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Initialize embedder
	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	require.NoError(t, err)
	backend := &store.Backend{}
	for _, l := range state.Get(ctx) {
		backend = &l.Backend
		break
	}
	require.NoError(t, store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backend.ID))
	time.Sleep(time.Second)
	// Test GetProvider
	provider, err := embedder.GetProvider(ctx)
	require.NoError(t, err)

	// Verify provider properties
	require.True(t, provider.CanEmbed())
	require.Equal(t, "all-minilm:33m", provider.ModelName())
	require.Contains(t, provider.GetBackendIDs(), backend.BaseURL)
}

func TestGetProvider_NoBackends(t *testing.T) {
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	require.NoError(t, err)

	_, err = embedder.GetProvider(ctx)
	require.Error(t, err)
	require.EqualError(t, err, "no backends found")
}

func TestGetRuntime_Adapter(t *testing.T) {
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	require.NoError(t, err)

	// Verify runtime adapter
	runtimeAdapter := embedder.GetRuntime(ctx)
	require.NotNil(t, runtimeAdapter)

	providers, err := runtimeAdapter(ctx, "Ollama")
	require.NoError(t, err)
	require.NotEmpty(t, providers)
}

func TestEmbeddingLifecycle(t *testing.T) {
	ctx, config, dbInstance, state, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Setup test backend
	backend := &store.Backend{}
	for _, l := range state.Get(ctx) {
		backend = &l.Backend
	}
	// Initialize embedder
	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	require.NoError(t, err)

	require.NoError(t, store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backend.ID))
	time.Sleep(time.Second * 30)
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
	}, 2*time.Minute, 100*time.Millisecond)
	time.Sleep(time.Second * 30)

	// Get provider and test embedding
	provider, err := embedder.GetProvider(ctx)
	require.NoError(t, err)

	embedClient, err := provider.GetEmbedConnection(backend.BaseURL)
	require.NoError(t, err)

	// Test embedding
	embeddings, err := embedClient.Embed(context.Background(), "test text")
	require.NoError(t, err)
	require.NotEmpty(t, embeddings)
}
