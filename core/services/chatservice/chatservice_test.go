package chatservice_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/chatservice"
	"github.com/js402/cate/core/services/tokenizerservice"
	"github.com/stretchr/testify/require"
)

func TestChat(t *testing.T) {
	ctx, backendState, dbInstance, cleanup := chatservice.SetupTestEnvironment(t)
	defer cleanup()
	userSubjectID := serverops.DefaultAdminUser
	err := store.New(dbInstance.WithoutTransaction()).CreateUser(ctx, &store.User{
		ID:           uuid.NewString(),
		FriendlyName: "John Doe",
		Email:        "string@strings.com",
		Subject:      userSubjectID,
	})
	require.NoError(t, err)
	tokenizer := tokenizerservice.MockTokenizer{}
	t.Run("creating new chat instance", func(t *testing.T) {
		manager := chatservice.New(backendState, dbInstance, tokenizer)

		// Test valid model
		id, err := manager.NewInstance(ctx, "user1", "smollm2:135m")
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, id)
	})

	t.Run("simple chat interaction tests", func(t *testing.T) {
		manager := chatservice.New(backendState, dbInstance, tokenizer)

		id, err := manager.NewInstance(ctx, "user1", "smollm2:135m")
		require.NoError(t, err)
		response, err := manager.Chat(ctx, id, "what is the capital of england?", "smollm2:135m")
		require.NoError(t, err)
		responseLower := strings.ToLower(response)
		println(responseLower)
		require.Contains(t, responseLower, "london")
	})

	t.Run("test chat history via interactions", func(t *testing.T) {
		manager := chatservice.New(backendState, dbInstance, tokenizer)

		// Create new chat instance
		id, err := manager.NewInstance(ctx, "user1", "smollm2:135m")
		require.NoError(t, err)

		// Verify initial empty history
		history, err := manager.GetChatHistory(ctx, id)
		require.NoError(t, err)
		require.Len(t, history, 0, "new instance should have empty history")

		// First interaction
		userMessage1 := "What's the capital of France?"
		_, err = manager.Chat(ctx, id, userMessage1, "smollm2:135m")
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
		_, err = manager.Chat(ctx, id, userMessage2, "smollm2:135m")
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
}

// TestLongConversation simulates a more extended interaction with the chat service.
func TestLongConversation(t *testing.T) {
	ctx, backendState, dbInstance, cleanup := chatservice.SetupTestEnvironment(t)
	defer cleanup()

	// repo, cleanup2, err := messagerepo.NewTestStore(t)
	// require.NoError(t, err, "failed to initialize test repository")
	// defer cleanup2()

	tokenizer := tokenizerservice.MockTokenizer{}
	userSubjectID := serverops.DefaultAdminUser
	err := store.New(dbInstance.WithoutTransaction()).CreateUser(ctx, &store.User{
		ID:           uuid.NewString(),
		FriendlyName: "John Doe",
		Email:        "string@strings.com",
		Subject:      userSubjectID,
	})
	require.NoError(t, err)
	t.Run("simulate extended chat conversation", func(t *testing.T) {
		manager := chatservice.New(backendState, dbInstance, tokenizer)

		model := "smollm2:135m"
		subject := "user-long-convo"

		instanceID, err := manager.NewInstance(ctx, subject, model)
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

		for i, userMsg := range userMessages {
			t.Logf("====================================================================================\n")
			t.Logf("Sending message %d: %s \n", i+1, userMsg)
			response, err := manager.Chat(ctx, instanceID, userMsg, model)

			require.NoError(t, err, "Chat interaction failed for message %d", i+1)
			require.NotEmpty(t, response, "Assistant response should not be empty for message %d", i+1)
			require.Greater(t, len(response), 5)
			t.Logf("Received response %d: %s \n", i+1, response)
			t.Logf("====================================================================================\n")
		}

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
