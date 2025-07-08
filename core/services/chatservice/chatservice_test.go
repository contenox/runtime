package chatservice_test

import (
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/chatservice"
	"github.com/contenox/runtime-mvp/core/services/testingsetup"
	"github.com/contenox/runtime-mvp/core/services/tokenizerservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSystem_ChatService_FullLifecycleWithHistoryAndModelInference(t *testing.T) {
	ctx, backendState, dbInstance, cleanup, err := testingsetup.New(t.Context(), serverops.NewLogActivityTracker(slog.Default())).
		WithTriggerChan().
		WithServiceManager(&serverops.Config{JWTExpiry: "1h"}).
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
		Build().Unzip()
	defer cleanup()
	require.NoError(t, err)

	tokenizer := tokenizerservice.MockTokenizer{}
	settings := kv.NewLocalCache(dbInstance, "test:")
	manager := chat.New(backendState, tokenizer, settings)
	chatHook := hooks.NewChatHook(dbInstance, manager)
	echocmd := hooks.NewEchoHook()

	// Mux for handling commands like /echo
	hookMux := hooks.NewMux(map[string]taskengine.HookRepo{
		"echo": echocmd,
	})

	// Combine all hooks into one registry
	hooks := hooks.NewSimpleProvider(map[string]taskengine.HookRepo{
		"command_router":            hookMux,
		"append_user_message":       chatHook,
		"execute_model_on_messages": chatHook,
		"persist_messages":          chatHook,
	})

	exec, err := taskengine.NewExec(ctx, &llmrepo.MockModelRepo{}, hooks, serverops.NoopTracker{})
	if err != nil {
		log.Fatalf("initializing task engine engine failed: %v", err)
	}
	environmentExec, err := taskengine.NewEnv(ctx, serverops.NewLogActivityTracker(slog.Default()), exec)
	if err != nil {
		log.Fatalf("initializing task engine failed: %v", err)
	}
	t.Run("creating new chat instance", func(t *testing.T) {
		manager := chatservice.New(dbInstance, environmentExec)

		// Test valid model
		id, err := manager.NewInstance(ctx, "user1")
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, id)
	})

	t.Run("simple chat interaction tests", func(t *testing.T) {
		manager := chatservice.New(dbInstance, environmentExec)

		id, err := manager.NewInstance(ctx, "user1")
		require.NoError(t, err)
		req := chatservice.ChatRequest{
			SubjectID:           id,
			Message:             "what is the capital of england?",
			PreferredModelNames: []string{"smollm2:135m"},
		}
		response, _, _, err := manager.Chat(ctx, req)
		require.NoError(t, err)
		responseLower := strings.ToLower(response)
		println(responseLower)
		require.Contains(t, responseLower, "london")
	})

	t.Run("simple echo command interaction tests", func(t *testing.T) {
		manager := chatservice.New(dbInstance, environmentExec)

		id, err := manager.NewInstance(ctx, "user1")
		require.NoError(t, err)
		req := chatservice.ChatRequest{
			SubjectID:           id,
			Message:             "/echo hello world! 123",
			PreferredModelNames: []string{"smollm2:135m"},
		}
		response, _, _, err := manager.Chat(ctx, req)
		require.NoError(t, err)
		responseLower := strings.ToLower(response)
		println(responseLower)
		require.Equal(t, responseLower, "hello world! 123")
	})

	t.Run("test chat history via interactions", func(t *testing.T) {
		manager := chatservice.New(dbInstance, environmentExec)

		// Create new chat instance
		id, err := manager.NewInstance(ctx, "user1")
		require.NoError(t, err)

		// Verify initial empty history
		history, err := manager.GetChatHistory(ctx, id)
		require.NoError(t, err)
		require.Len(t, history, 0, "new instance should have empty history")

		// First interaction
		userMessage1 := "What's the capital of France?"
		req := chatservice.ChatRequest{
			SubjectID:           id,
			Message:             userMessage1,
			PreferredModelNames: []string{"smollm2:135m"},
		}
		_, _, _, err = manager.Chat(ctx, req)
		require.NoError(t, err)
		time.Sleep(time.Millisecond)
		// Verify first pair of messages
		history, err = manager.GetChatHistory(ctx, id)
		require.NoError(t, err)
		require.Len(t, history, 2, "should have user + assistant messages")

		// Check user message details
		userMsg := history[0]
		require.Equal(t, "user", userMsg.Role)
		require.Equal(t, userMessage1, userMsg.Content)
		require.True(t, userMsg.IsUser)
		require.False(t, userMsg.IsLatest)
		require.False(t, userMsg.SentAt.IsZero())

		// Check assistant message details
		assistantMsg := history[1]
		require.Equal(t, "assistant", assistantMsg.Role)
		require.NotEmpty(t, assistantMsg.Content)
		require.False(t, assistantMsg.IsUser)
		require.True(t, assistantMsg.IsLatest)
		require.True(t, assistantMsg.SentAt.After(userMsg.SentAt))

		// Second interaction
		userMessage2 := "What about Germany?"
		req = chatservice.ChatRequest{
			SubjectID:           id,
			Message:             userMessage2,
			PreferredModelNames: []string{"smollm2:135m"},
		}
		_, _, _, err = manager.Chat(ctx, req)
		require.NoError(t, err)

		// Verify updated history
		history, err = manager.GetChatHistory(ctx, id)
		require.NoError(t, err)
		require.Len(t, history, 4, "should accumulate messages")

		// Verify message order and flags
		secondUserMsg := history[2]
		require.Equal(t, userMessage2, secondUserMsg.Content)
		require.True(t, secondUserMsg.SentAt.After(assistantMsg.SentAt))

		finalAssistantMsg := history[3]
		require.True(t, finalAssistantMsg.IsLatest)
		require.Contains(t, strings.ToLower(finalAssistantMsg.Content), "germany", "should maintain context")

		// Verify all timestamps are sequential
		for i := range history {
			if i == len(history)-1 {
				break
			}
			require.True(t, history[i+1].SentAt.After(history[i].SentAt),
				"message %d should be after message %d", i, i)
		}

		// Test invalid ID case
		hist, err := manager.GetChatHistory(ctx, uuid.New().String())
		require.NoError(t, err)
		require.Len(t, hist, 0)
	})

	t.Run("simulate extended chat conversation", func(t *testing.T) {
		manager := chatservice.New(dbInstance, environmentExec)

		subject := "user-long-convo"

		instanceID, err := manager.NewInstance(ctx, subject)
		require.NoError(t, err, "Failed to create new chat instance")
		require.NotEqual(t, uuid.Nil, instanceID, "Instance ID should not be nil")

		userMessages := []string{
			"Hi there! Can you tell me about Large Language Models?",
			"What are some common applications of LLMs?",
			"Explain the concept of 'fine-tuning' in the context of LLMs.",
			"Are there any ethical considerations when developing or using LLMs?",
			"How do LLMs actually generate text?",
			"What's the difference between a transformer model and other types of neural networks?",
			"Can you give me an example of a prompt and a possible completion?",
			"Thanks for the information!",
			"Can you recommend any resources for learning more about LLMs?",
			"Tell me more about the history of LLMs.",
			"So can you give me an example of a prompt and a possible completion?",
			"How does working in a prompt affect the output of an LLM?",
			"Why do same prompts produce different outputs?",
		}
		startTime := time.Now().UTC()
		tokens := 0
		for i, userMsg := range userMessages {
			t.Logf("====================================================================================\n")
			t.Logf("Sending message %d: %s \n", i+1, userMsg)
			req := chatservice.ChatRequest{
				SubjectID:           instanceID,
				Message:             userMsg,
				PreferredModelNames: []string{"smollm2:135m"},
			}
			response, inputtokenCount, outputtokencount, err := manager.Chat(ctx, req)
			tokens += inputtokenCount + outputtokencount
			require.NoError(t, err, "Chat interaction failed for message %d", i+1)
			require.NotEmpty(t, response, "Assistant response should not be empty for message %d", i+1)
			require.Greater(t, len(response), 5)
			t.Logf("Received response %d: %s \n", i+1, response)
			t.Logf("====================================================================================\n")
		}
		finishTime := time.Now().UTC()
		duration := finishTime.Sub(startTime)
		tokenPerSecond := float64(tokens) / duration.Seconds()
		t.Logf("tokens/second %v", tokenPerSecond)
		require.GreaterOrEqual(t, tokenPerSecond, float64(1), "Token rate should be at least 1 tokens per second was %v", tokenPerSecond)
		history, err := manager.GetChatHistory(ctx, instanceID)
		require.NoError(t, err, "Failed to get chat history")

		expectedMessageCount := len(userMessages) * 2
		was := ""
		for _, message := range history {
			trimIndex := min(len(message.Content), 12)
			was += message.Role + ": " + message.Content[:trimIndex] + "\n"
		}
		require.Len(t, history, expectedMessageCount, "conversation was: %s", was)

		require.GreaterOrEqual(t, len(history), 1, "History should not be empty")
		lastMessage := history[len(history)-1]
		require.Equal(t, "assistant", lastMessage.Role, "The last message should be from the assistant")
		require.False(t, lastMessage.IsUser, "Last message should not be a user message")
		require.True(t, lastMessage.IsLatest, "The last message should be marked as the latest")
		require.NotEmpty(t, lastMessage.Content, "Last assistant message content should not be empty")

		for i := 1; i < len(history); i++ {
			require.True(t, history[i].SentAt.After(history[i-1].SentAt) || history[i].SentAt.Equal(history[i-1].SentAt),
				"message %d timestamp (%v) should be at or after message %d timestamp (%v)", i, history[i].SentAt, i-1, history[i-1].SentAt)
		}
	})
}
