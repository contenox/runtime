package modelprovider

import (
	"context"
	"fmt"

	"github.com/contenox/contenox/core/serverops"
)

// MockProvider implements the Provider interface for testing
type MockProvider struct {
	ID            string
	Name          string
	ContextLength int
	CanChatFlag   bool
	CanEmbedFlag  bool
	CanStreamFlag bool
	CanPromptFlag bool
	Backends      []string
}

func (m *MockProvider) GetBackendIDs() []string {
	return m.Backends
}

func (m *MockProvider) ModelName() string {
	return m.Name
}

func (m *MockProvider) GetID() string {
	return m.ID
}

func (m *MockProvider) GetContextLength() int {
	return m.ContextLength
}

func (m *MockProvider) CanChat() bool {
	return m.CanChatFlag
}

func (m *MockProvider) CanEmbed() bool {
	return m.CanEmbedFlag
}

func (m *MockProvider) CanStream() bool {
	return m.CanStreamFlag
}

func (m *MockProvider) CanPrompt() bool {
	return m.CanPromptFlag
}

func (m *MockProvider) GetChatConnection(backendID string) (serverops.LLMChatClient, error) {
	return &MockChatClient{ProviderID: m.ID}, nil
}

func (m *MockProvider) GetEmbedConnection(backendID string) (serverops.LLMEmbedClient, error) {
	return &MockEmbedClient{ProviderID: m.ID}, nil
}

func (m *MockProvider) GetStreamConnection(backendID string) (serverops.LLMStreamClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockProvider) GetPromptConnection(backendID string) (serverops.LLMPromptExecClient, error) {
	return &MockPromptClient{ProviderID: m.ID}, nil
}

// Mock clients for testing
type MockChatClient struct {
	ProviderID string
}

func (m *MockChatClient) Chat(ctx context.Context, messages []serverops.Message) (serverops.Message, error) {
	return serverops.Message{}, nil
}

type MockEmbedClient struct {
	ProviderID string
}

func (m *MockEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	return []float64{}, nil
}

type MockPromptClient struct {
	ProviderID string
}

func (m *MockPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	return "", nil
}
