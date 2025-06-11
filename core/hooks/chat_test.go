package hooks_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/contenox/core/chat"
	"github.com/contenox/contenox/core/hooks"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/services/tokenizerservice"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSystemChatHooks(t *testing.T) {
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

	// Initialize chat manager with mock tokenizer
	tokenizer := tokenizerservice.MockTokenizer{}
	chatManager := chat.New(backendState, tokenizer)
	chatHook := hooks.NewChatHook(dbInstance, chatManager)
	// Generate unique subject ID for this test session
	subjectID := uuid.New().String()
	err = store.New(dbInstance.WithoutTransaction()).CreateMessageIndex(t.Context(), subjectID, serverops.DefaultAdminUser)
	require.NoError(t, err)
	userMessage := "What's the capital of France?"

	t.Run("chat_hooks", func(t *testing.T) {
		hookCall := &taskengine.HookCall{
			Type: "append_user_input",
			Args: map[string]string{"subject_id": subjectID},
		}

		// Execute hook
		status, result, dataType, err := chatHook.Exec(
			ctx,
			time.Now().UTC(),
			userMessage,
			taskengine.DataTypeString,
			hookCall,
		)

		// Validate results
		require.NoError(t, err)
		require.Equal(t, taskengine.StatusSuccess, status)
		require.Equal(t, taskengine.DataTypeChatHistory, dataType)

		history, ok := result.(taskengine.ChatHistory)
		messages := history.Messages
		require.True(t, ok)
		require.Len(t, messages, 1)
		require.Equal(t, "user", messages[0].Role)
		require.Equal(t, userMessage, messages[0].Content)

		hookCall = &taskengine.HookCall{
			Type: "execute_chat_model",
			Args: map[string]string{"model": "smollm2:135m"},
		}

		// Execute hook
		status, result, dataType, err = chatHook.Exec(
			ctx,
			time.Now().UTC(),
			result,
			taskengine.DataTypeChatHistory,
			hookCall,
		)

		// Validate results
		require.NoError(t, err)
		require.Equal(t, taskengine.StatusSuccess, status)
		require.Equal(t, taskengine.DataTypeChatHistory, dataType)

		updatedHistory, ok := result.(taskengine.ChatHistory)
		updatedMessages := updatedHistory.Messages
		require.True(t, ok)
		require.Len(t, updatedMessages, 2)
		require.Equal(t, "user", updatedMessages[0].Role)
		require.Equal(t, "assistant", updatedMessages[1].Role)
		require.NotEmpty(t, updatedMessages[1].Content)

		hookCall = &taskengine.HookCall{
			Type: "persist_input_output",
			Args: map[string]string{"subject_id": subjectID},
		}

		// Execute hook
		status, result, dataType, err = chatHook.Exec(
			ctx,
			time.Now().UTC(),
			result,
			taskengine.DataTypeChatHistory,
			hookCall,
		)
		// Validate results
		require.NoError(t, err)
		require.Equal(t, taskengine.StatusSuccess, status)
		require.Equal(t, taskengine.DataTypeChatHistory, dataType)
		t.Log(result)

		// Verify database persistence
		persistedMessages, err := chatManager.ListMessages(ctx, dbInstance.WithoutTransaction(), subjectID)
		require.NoError(t, err)
		require.Len(t, persistedMessages, 2)
		require.Equal(t, "user", persistedMessages[0].Role)
		require.Equal(t, "assistant", persistedMessages[1].Role)
	})
}
