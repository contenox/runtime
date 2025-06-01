package llmrepo_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
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
		return strings.Contains(dst.String(), `"name":"granite-embedding:30m"`)
	}, 1*time.Minute, 1*time.Second)

	provider, err := embedder.GetProvider(ctx)
	require.NoError(b, err)

	embedClient, err := provider.GetEmbedConnection(backend.BaseURL)
	require.NoError(b, err)
	testText := "This is a benchmark test string for measuring embedding performance"
	tokenCount := 13
	require.NoError(b, err)

	var totalTokens int64
	var totalLatency time.Duration

	// Warmup the embedding client
	_, err = embedClient.Embed(ctx, testText)
	require.NoError(b, err)

	// Reset timer after all setup
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		start := time.Now().UTC()
		_, err := embedClient.Embed(ctx, testText)
		require.NoError(b, err)
		elapsed := time.Since(start)

		atomic.AddInt64(&totalTokens, int64(tokenCount))
		atomic.AddInt64((*int64)(&totalLatency), elapsed.Nanoseconds())
	}

	// Compute and report additional metrics
	totalLatencySeconds := float64(totalLatency) / float64(time.Second)
	tokensPerSecond := float64(totalTokens) / totalLatencySeconds
	reqsPerSecond := float64(b.N) / totalLatencySeconds

	b.ReportMetric(totalLatencySeconds, "avg-sec/request")
	b.ReportMetric(tokensPerSecond, "tokens/sec")
	b.ReportMetric(reqsPerSecond, "reqs/sec")
}
