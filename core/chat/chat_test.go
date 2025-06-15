package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/contenox/core/chat"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/services/tokenizerservice"
	"github.com/stretchr/testify/require"
)

func TestManagerSystem(t *testing.T) {
	const userMessage = "What is the capital of France?"
	const systemMessage = "You are a helpful assistant."
	const subjectID = "test-subject-id-12345"

	// Shared setup across all sub-tests
	tenv := testingsetup.New(context.Background(), serverops.NoopTracker{}).
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
		Build()

	ctx, backendState, dbInstance, cleanup, err := tenv.Unzip()
	require.NoError(t, err)
	defer cleanup()

	tokenizer := tokenizerservice.MockTokenizer{}
	manager := chat.New(backendState, tokenizer)

	// Ensure message index exists
	err = store.New(dbInstance.WithoutTransaction()).CreateMessageIndex(ctx, subjectID, serverops.DefaultAdminUser)
	require.NoError(t, err)

	t.Run("AddInstruction_inserts_system_message", func(t *testing.T) {
		err := manager.AddInstruction(ctx, dbInstance.WithoutTransaction(), subjectID, systemMessage)
		require.NoError(t, err)

		messages, err := manager.ListMessages(ctx, dbInstance.WithoutTransaction(), subjectID)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, "system", messages[0].Role)
		require.Equal(t, systemMessage, messages[0].Content)
	})

	t.Run("AppendMessage_adds_user_message_to_history", func(t *testing.T) {
		initial := []serverops.Message{
			{Role: "system", Content: systemMessage},
		}

		newHistory, err := manager.AppendMessage(ctx, initial, userMessage, "user")
		require.NoError(t, err)
		require.Len(t, newHistory, 2)
		require.Equal(t, "user", newHistory[1].Role)
		require.Equal(t, userMessage, newHistory[1].Content)
	})

	t.Run("ListMessages_returns_all_messages_from_db", func(t *testing.T) {
		messages, err := manager.ListMessages(ctx, dbInstance.WithoutTransaction(), subjectID)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, "system", messages[0].Role)
		require.Equal(t, systemMessage, messages[0].Content)
	})

	t.Run("AppendMessages_persists_user_and_assistant_messages", func(t *testing.T) {
		beginTime := time.Now().UTC()
		inputMsg := &serverops.Message{
			Role:    "user",
			Content: userMessage,
		}
		responseMsg := &serverops.Message{
			Role:    "assistant",
			Content: "The capital of France is Paris.",
		}

		err := manager.AppendMessages(ctx, dbInstance.WithoutTransaction(), beginTime, subjectID, inputMsg, responseMsg)
		require.NoError(t, err)

		storedMessages, err := manager.ListMessages(ctx, dbInstance.WithoutTransaction(), subjectID)
		require.NoError(t, err)
		require.Len(t, storedMessages, 3) // +1 from AddInstruction
		require.Equal(t, "user", storedMessages[1].Role)
		require.Equal(t, userMessage, storedMessages[1].Content)
		require.Equal(t, "assistant", storedMessages[2].Role)
		require.Equal(t, "The capital of France is Paris.", storedMessages[2].Content)
	})

	t.Run("ChatExec_runs_llm_with_chat_history_and_returns_response", func(t *testing.T) {
		history := []serverops.Message{
			{Role: "system", Content: systemMessage},
			{Role: "user", Content: userMessage},
		}

		resp, inputTokens, outputTokens, _, err := manager.ChatExec(ctx, history, 4096, "smollm2:135m")
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Greater(t, inputTokens, 0)
		require.Greater(t, outputTokens, 0)
		require.Equal(t, "assistant", resp.Role)
		require.NotEmpty(t, resp.Content)
	})

	t.Run("ChatExec_fails_when_no_messages_given", func(t *testing.T) {
		resp, _, _, _, err := manager.ChatExec(ctx, nil, 4096, "smollm2:135m")
		require.Error(t, err)
		require.Nil(t, resp)
		require.EqualError(t, err, "no messages provided")
	})

	t.Run("ChatExec_fails_when_last_message_is_not_user", func(t *testing.T) {
		history := []serverops.Message{
			{Role: "assistant", Content: "I don't know."},
		}
		resp, _, _, _, err := manager.ChatExec(ctx, history, 4096, "smollm2:135m")
		require.Error(t, err)
		require.Nil(t, resp)
		require.EqualError(t, err, "last message must be from user or system was assistant")
	})

	t.Run("CalculateContextSize_estimates_token_count_for_prompt", func(t *testing.T) {
		history := []serverops.Message{
			{Role: "user", Content: "What is life?"},
			{Role: "user", Content: "Explain quantum physics."},
		}

		size, err := manager.CalculateContextSize(ctx, history, "phi-3")
		require.NoError(t, err)
		require.Greater(t, size, 0)
	})
}
