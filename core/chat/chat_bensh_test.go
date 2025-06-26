package chat_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/testingsetup"
	"github.com/contenox/runtime-mvp/core/services/tokenizerservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/stretchr/testify/require"
)

func BenchmarkChatExec(b *testing.B) {
	const modelName = "smollm2:135m"
	const initialUserMessage = "What is the capital of France?"

	// Setup test environment
	tenv := testingsetup.New(b.Context(), serverops.NoopTracker{}).
		WithTriggerChan().
		WithServiceManager(&serverops.Config{JWTExpiry: "1h"}).
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		WithModel(modelName).
		RunState().
		RunDownloadManager().
		WithDefaultUser().
		WaitForModel(modelName).
		Build()

	ctx, backendState, _, cleanup, err := tenv.Unzip()
	require.NoError(b, err)
	defer cleanup()

	tokenizer := tokenizerservice.MockTokenizer{}
	settings := kv.NewLocalCache(tenv.GetDBInstance(), "test:")

	manager := chat.New(backendState, tokenizer, settings)

	b.Log("Warmup")
	_, _, _, _, warmupErr := manager.ChatExec(ctx, []taskengine.Message{
		{Role: "user", Content: initialUserMessage},
	}, []string{"ollama"}, modelName)
	require.NoError(b, warmupErr)

	// Start short conversation
	convo := []taskengine.Message{
		{Role: "user", Content: initialUserMessage},
	}

	var totalInputTokens, totalOutputTokens, totalTokens int64
	var totalLatency int64

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		start := time.Now()

		resp, inputTok, outputTok, _, err := manager.ChatExec(ctx, convo, []string{"ollama"}, modelName)
		require.NoError(b, err)
		require.NotNil(b, resp)
		require.Greater(b, inputTok+outputTok, 0)

		duration := time.Since(start)

		// Simulate next turn in conversation
		convo = append(convo, *resp)
		convo = append(convo, taskengine.Message{Role: "user", Content: "Can you tell me more?"})

		// Track metrics
		atomic.AddInt64(&totalInputTokens, int64(inputTok))
		atomic.AddInt64(&totalOutputTokens, int64(outputTok))
		atomic.AddInt64(&totalTokens, int64(inputTok+outputTok))
		atomic.AddInt64(&totalLatency, duration.Nanoseconds())

		// Optional: Log progress every 5 steps
		if i%5 == 0 {
			b.Logf("Iter %d: %.2f tokens/sec", i, float64(totalTokens)/float64(totalLatency)*float64(time.Second))
		}
	}

	// Final metrics
	latencySeconds := float64(totalLatency) / float64(time.Second)
	tokensPerSecond := float64(totalTokens) / latencySeconds
	reqsPerSecond := float64(b.N) / latencySeconds
	inputTokensPerSecond := float64(totalInputTokens) / latencySeconds
	outputTokensPerSecond := float64(totalOutputTokens) / latencySeconds

	b.ReportMetric(tokensPerSecond, "tokens/sec")
	b.ReportMetric(inputTokensPerSecond, "inputtokens/sec")
	b.ReportMetric(outputTokensPerSecond, "outputtokens/sec")
	b.ReportMetric(reqsPerSecond, "reqs/sec")
	b.ReportMetric(latencySeconds/float64(b.N), "avg-sec/request")
}
