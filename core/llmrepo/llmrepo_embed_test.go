package llmrepo_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/stretchr/testify/require"
)

func BenchmarkEmbedding(b *testing.B) {
	config, env := setupTestEnvironment()
	require.NoError(b, env.Err)
	defer env.Cleanup()

	embedder, err := env.NewEmbedder(config)
	require.NoError(b, err)
	require.NoError(b, env.AssignBackends(serverops.EmbedPoolID).Err)
	require.NoError(b, env.WaitForModel(config.EmbedModel).Err)

	provider, err := embedder.GetProvider(env.Ctx)
	require.NoError(b, err)

	embedClient, err := env.GetEmbedConnection(provider)
	require.NoError(b, err)
	testText := "This is a benchmark test string for measuring embedding performance"
	tokenCount := 13
	require.NoError(b, err)

	var totalTokens int64
	var totalLatency time.Duration

	// Warmup the embedding client
	_, err = embedClient.Embed(env.Ctx, testText)
	require.NoError(b, err)

	// Reset timer after all setup
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		start := time.Now().UTC()
		_, err := embedClient.Embed(env.Ctx, testText)
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
