package modelprovider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystem_OllamaProvider_ChatIntegration(t *testing.T) {
	ctx, backendState, _, cleanup, err := testingsetup.New(context.Background(), serverops.NoopTracker{}).
		WithTriggerChan().
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
	runtime := backendState.Get(ctx)
	url := ""
	for _, state := range runtime {
		url = state.Backend.BaseURL
	}
	require.NotEmpty(t, url, "Failed to get backend URL from test setup")
	provider := modelprovider.NewOllamaModelProvider("smollm2:135m", []string{url}, modelprovider.WithChat(true))
	require.True(t, provider.CanChat())

	client, err := provider.GetChatConnection(url)
	require.NoError(t, err)
	require.NotNil(t, client)
	t.Run("SinglePrompt_ShouldReturnAssistantReply", func(t *testing.T) {
		// First chat call
		response, err := client.Chat(ctx, []serverops.Message{
			{Content: "Hello, world!", Role: "user"},
		})
		require.NoError(t, err)
		t.Logf("Response 1: %s", response.Content)
		require.NotEmpty(t, response.Content)
		require.Equal(t, "assistant", response.Role)
		response2, err := client.Chat(ctx, []serverops.Message{
			{Content: "Hello, world!", Role: "user"},
			response,
			{Content: "How are you?", Role: "user"},
		})
		require.NoError(t, err)
		t.Logf("Response 2: %s", response2.Content)
		require.NotEmpty(t, response2.Content)
		require.Equal(t, "assistant", response2.Role)
	})
	t.Run("MultiTurnConversation_ShouldMaintainState", func(t *testing.T) {
		userMessages := []serverops.Message{
			{Content: "Hello, world!", Role: "user"},
			{Content: "How are you?", Role: "user"},
			{Content: "How old are you?", Role: "user"},
			{Content: "Hey", Role: "user"},
			{Content: "Where are you from?", Role: "user"},
			{Content: "What is your favorite color?", Role: "user"},
			{Content: "What is your favorite food?", Role: "user"},
			{Content: "What is your favorite movie?", Role: "user"},
			{Content: "What is your favorite sport?", Role: "user"},
		}
		conversation := func(chat []serverops.Message, prompt string) []serverops.Message {
			chat = append(chat, serverops.Message{Role: "user", Content: prompt})
			response, err := client.Chat(ctx, chat)
			require.NoError(t, err)
			require.NotEmpty(t, response.Content)
			require.Equal(t, "assistant", response.Role)
			fmt.Printf("Response: %s /n", response.Content)

			chat = append(chat, response)
			return chat
		}
		chat := []serverops.Message{}
		for _, message := range userMessages {
			chat = conversation(chat, message.Content)
		}
	})

	t.Run("LargePrompt_ShouldReturnEmptyOrError", func(t *testing.T) {
		// Generate a huge input string
		hugeInput := make([]byte, 100_000)
		for i := range hugeInput {
			hugeInput[i] = 'a'
		}
		hugeMessage := string(hugeInput)

		// Send huge input to chat client
		response, err := client.Chat(ctx, []serverops.Message{
			{Content: hugeMessage, Role: "user"},
		})
		if err == nil {
			t.Fatalf("expected an error %s", response.Content)
		}
		if err != nil {
			t.Logf("Expected error for huge input: %v", err)
		}
		assert.Contains(t, err.Error(), "empty content from model", "Error should come from the ollama API", response.Content)
	})
	t.Run("InvalidModel_ShouldReturnDescriptiveError", func(t *testing.T) {
		nonExistentModel := "this-model-definitely-does-not-exist-12345:latest"
		badProvider := modelprovider.NewOllamaModelProvider(nonExistentModel, []string{url}, modelprovider.WithChat(true))
		require.True(t, badProvider.CanChat())

		invalidClient, err := badProvider.GetChatConnection(url)
		require.NoError(t, err, "Getting the client should succeed even if model doesn't exist yet")
		require.NotNil(t, invalidClient)

		_, err = invalidClient.Chat(ctx, []serverops.Message{
			{Content: "Does not matter", Role: "user"},
		})

		require.Error(t, err, "Expected an error when chatting with a non-existent model")
		assert.ErrorContains(t, err, "ollama API chat request failed", "Error message should indicate API failure")
		assert.ErrorContains(t, err, nonExistentModel, "Error message should mention the problematic model name")
		t.Logf("Confirmed error for non-existent model: %v", err)
	})

}

func TestUnit_OllamaProvider_RejectsChatWhenDisabled(t *testing.T) {
	dummyURL := "http://localhost:11434"
	backends := []string{dummyURL}
	modelName := "smollm2:135m"

	provider := modelprovider.NewOllamaModelProvider(modelName, backends, modelprovider.WithChat(false))
	require.False(t, provider.CanChat(), "Provider should report CanChat as false")
	client, err := provider.GetChatConnection(dummyURL)
	require.Error(t, err, "Expected an error when getting chat connection for a non-chat provider")
	assert.Nil(t, client, "Client should be nil on error")
	assert.ErrorContains(t, err, "does not support chat", "Error message should indicate lack of chat support")
}
