package libmodelprovider

import (
	"context"
	"fmt"
	"net/http"
)

var _ Provider = (*OpenAIProvider)(nil)

type OpenAIProvider struct {
	id            string
	apiKey        string
	modelName     string
	baseURL       string
	httpClient    *http.Client
	contextLength int
	canChat       bool
	canPrompt     bool
	canEmbed      bool
	canStream     bool
}

func NewOpenAIProvider(apiKey, modelName string, backendURLs []string, httpClient *http.Client) *OpenAIProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	chatModels := map[string]bool{
		"gpt-4o":        true,
		"gpt-4o-mini":   true,
		"gpt-4-turbo":   true,
		"gpt-3.5-turbo": true,
	}
	embeddingModels := map[string]bool{
		"text-embedding-ada-002": true,
		"text-embedding-3-small": true,
		"text-embedding-3-large": true,
	}
	promptModels := map[string]bool{
		"gpt-3.5-turbo-instruct": true,
	}
	if len(backendURLs) == 0 {
		backendURLs = []string{"https://api.openai.com/v1"}
	}
	provider := &OpenAIProvider{
		id:            "openai",
		apiKey:        apiKey,
		modelName:     modelName,
		baseURL:       backendURLs[0],
		httpClient:    httpClient,
		contextLength: 8192,
		canChat:       chatModels[modelName],
		canPrompt:     promptModels[modelName],
		canEmbed:      embeddingModels[modelName],
		canStream:     true,
	}

	switch modelName {
	case "gpt-4o", "gpt-4o-mini":
		provider.contextLength = 128000
	case "gpt-4-turbo":
		provider.contextLength = 128000
	case "gpt-3.5-turbo":
		provider.contextLength = 16385
	case "text-embedding-ada-002", "text-embedding-3-small", "text-embedding-3-large":
		provider.contextLength = 8192
	}

	return provider
}

func (p *OpenAIProvider) GetBackendIDs() []string {
	return []string{"default"}
}

func (p *OpenAIProvider) ModelName() string {
	return p.modelName
}

func (p *OpenAIProvider) GetID() string {
	return p.id
}

func (p *OpenAIProvider) GetType() string {
	return "openai"
}

func (p *OpenAIProvider) GetContextLength() int {
	return p.contextLength
}

func (p *OpenAIProvider) CanChat() bool {
	return p.canChat
}

func (p *OpenAIProvider) CanEmbed() bool {
	return p.canEmbed
}

func (p *OpenAIProvider) CanStream() bool {
	return p.canStream
}

func (p *OpenAIProvider) CanPrompt() bool {
	return p.canPrompt
}

func (p *OpenAIProvider) GetChatConnection(ctx context.Context, backendID string) (LLMChatClient, error) {
	if !p.CanChat() {
		return nil, fmt.Errorf("model %s does not support chat interactions", p.modelName)
	}
	return &openAIChatClient{
		openAIClient: openAIClient{
			baseURL:    p.baseURL,
			apiKey:     p.apiKey,
			httpClient: p.httpClient,
			modelName:  p.modelName,
			maxTokens:  p.contextLength,
		},
	}, nil
}

func (p *OpenAIProvider) GetPromptConnection(ctx context.Context, backendID string) (LLMPromptExecClient, error) {
	if !p.CanPrompt() {
		return nil, fmt.Errorf("model %s does not support prompt (legacy completion) interactions. Consider using Chat API", p.modelName)
	}
	// For OpenAI, prompt interactions are often handled via the chat API for modern models.
	// If you truly need text-completion (e.g., for 'gpt-3.5-turbo-instruct'),
	// you'd create a dedicated openAIPromptClient with `/v1/completions`.
	return &openAIPromptClient{
		openAIClient: openAIClient{
			baseURL:    p.baseURL,
			apiKey:     p.apiKey,
			httpClient: p.httpClient,
			modelName:  p.modelName,
			maxTokens:  p.contextLength,
		},
	}, nil
}

func (p *OpenAIProvider) GetEmbedConnection(ctx context.Context, backendID string) (LLMEmbedClient, error) {
	if !p.CanEmbed() {
		return nil, fmt.Errorf("model %s does not support embedding interactions", p.modelName)
	}
	return &openAIEmbedClient{
		openAIClient: openAIClient{
			baseURL:    p.baseURL,
			apiKey:     p.apiKey,
			httpClient: p.httpClient,
			modelName:  p.modelName,
		},
	}, nil
}

func (p *OpenAIProvider) GetStreamConnection(ctx context.Context, backendID string) (LLMStreamClient, error) {
	if !p.CanStream() {
		return nil, fmt.Errorf("model %s does not support streaming interactions", p.modelName)
	}
	return &openAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    p.baseURL,
			apiKey:     p.apiKey,
			httpClient: p.httpClient,
			modelName:  p.modelName,
			maxTokens:  p.contextLength,
		},
	}, nil
}
