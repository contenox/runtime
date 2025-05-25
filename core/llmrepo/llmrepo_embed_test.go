package llmrepo_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/stretchr/testify/require"
)

func BenchmarkEmbedding(b *testing.B) {
	ctx, config, dbInstance, state, cleanup, err := setupTestEnvironment()
	require.NoError(b, err)
	defer cleanup()

	embedder, err := llmrepo.NewEmbedder(ctx, config, dbInstance, state)
	require.NoError(b, err)

	// Assign backend before benchmark begins
	backend := &store.Backend{}
	for _, l := range state.Get(ctx) {
		backend = &l.Backend
	}
	require.NoError(b, store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backend.ID))
	time.Sleep(time.Second * 10)
	// Ensure the model is downloaded and ready
	require.Eventually(b, func() bool {
		currentState := state.Get(ctx)
		r, err := json.Marshal(currentState)
		if err != nil {
			b.Logf("error marshaling state: %v", err)
			return false
		}
		dst := &bytes.Buffer{}
		if err := json.Compact(dst, r); err != nil {
			b.Logf("error compacting JSON: %v", err)
			return false
		}
		return strings.Contains(dst.String(), `"name":"all-minilm:33m"`)
	}, 1*time.Minute, 1*time.Second)

	provider, err := embedder.GetProvider(ctx)
	require.NoError(b, err)

	embedClient, err := provider.GetEmbedConnection(backend.BaseURL)
	require.NoError(b, err)

	testText := "This is a benchmark test string for measuring embedding performance"

	// Only measure the loop, not setup
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := embedClient.Embed(ctx, testText)
		require.NoError(b, err)
	}
}
