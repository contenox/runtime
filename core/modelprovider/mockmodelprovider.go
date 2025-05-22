package modelprovider

import (
	"context"

	"github.com/contenox/contenox/core/serverops"
)

var _ Provider = (*MockProvider)(nil)

// Mock ModelProvider implementation for testing purposes.
type MockProvider struct {
	ID            string
	Name          string
	ContextLength int
	Backends      []string
	CanChatFlag   bool
	CanEmbedFlag  bool
	CanPromptFlag bool
	CanStreamFlag bool
}

// GetBackendIDs returns available backend IDs.
func (m *MockProvider) GetBackendIDs() []string {
	return m.Backends
}

// ModelName returns the provider model name.
func (m *MockProvider) ModelName() string {
	return m.Name
}

// GetID returns the unique identifier for the provider.
func (m *MockProvider) GetID() string {
	return m.ID
}

// GetContextLength returns the maximum context length.
func (m *MockProvider) GetContextLength() int {
	return m.ContextLength
}

// CanChat indicates whether chat functionality is supported.
func (m *MockProvider) CanChat() bool {
	return m.CanChatFlag
}

// CanEmbed indicates whether embedding functionality is supported.
func (m *MockProvider) CanEmbed() bool {
	return m.CanEmbedFlag
}

// CanStream indicates whether streaming is supported.
func (m *MockProvider) CanStream() bool {
	return m.CanStreamFlag
}

func (m *MockProvider) CanPrompt() bool {
	return m.CanPromptFlag
}

// GetChatConnection returns a mock LLMChatClient.
// Here we simply return a dummy implementation that meets the required interface.
func (m *MockProvider) GetChatConnection(backendID string) (serverops.LLMChatClient, error) {
	// In a real implementation this would create and return a connection to a chat LLM.
	return &mockChatClient{}, nil
}

// GetEmbedConnection returns a dummy LLMEmbedClient.
func (m *MockProvider) GetEmbedConnection(backendID string) (serverops.LLMEmbedClient, error) {
	return &mockEmbedClient{}, nil
}

// GetStreamConnection returns a dummy LLMStreamClient.
func (m *MockProvider) GetStreamConnection(backendID string) (serverops.LLMStreamClient, error) {
	return &mockStreamClient{}, nil
}

// GetPromptConnection implements Provider.
func (m *MockProvider) GetPromptConnection(backendID string) (serverops.LLMPromptExecClient, error) {
	return &mockPromptClient{}, nil
}

type mockChatClient struct{}

// Chat simulates a response by simply echoing the prompt.
func (m *mockChatClient) Chat(context.Context, []serverops.Message) (serverops.Message, error) {
	return serverops.Message{Role: "system", Content: "pong"}, nil
}

type mockEmbedClient struct{}

// Embed simulates embedding by returning a dummy vector.
func (m *mockEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

type mockStreamClient struct{}

// Stream simulates streaming by returning a channel that sends a fixed string.
func (m *mockStreamClient) Stream(ctx context.Context, prompt string) (<-chan string, error) {
	ch := make(chan string)
	go func() {
		defer close(ch)
		ch <- "streamed response for: " + prompt
	}()
	return ch, nil
}

type mockPromptClient struct{}

// Prompt simulates prompting by returning a dummy response.
func (m *mockPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	return "prompted response for: " + prompt, nil
}
