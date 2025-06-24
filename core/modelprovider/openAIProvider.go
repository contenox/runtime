package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/contenox/contenox/core/serverops"
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

func NewOpenAIProvider(id, apiKey, modelName string, httpClient *http.Client) (*OpenAIProvider, error) {
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

	provider := &OpenAIProvider{
		id:            id,
		apiKey:        apiKey,
		modelName:     modelName,
		baseURL:       "https://api.openai.com/v1",
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

	return provider, nil
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

func (p *OpenAIProvider) GetChatConnection(ctx context.Context, backendID string) (serverops.LLMChatClient, error) {
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

func (p *OpenAIProvider) GetPromptConnection(ctx context.Context, backendID string) (serverops.LLMPromptExecClient, error) {
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

func (p *OpenAIProvider) GetEmbedConnection(ctx context.Context, backendID string) (serverops.LLMEmbedClient, error) {
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

func (p *OpenAIProvider) GetStreamConnection(ctx context.Context, backendID string) (serverops.LLMStreamClient, error) {
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

type openAIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	modelName  string
	maxTokens  int
}

type openAIPromptClient struct {
	openAIClient
}

func (c *openAIPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	request := openAIChatRequest{
		Model:       c.modelName,
		Messages:    []serverops.Message{{Role: "user", Content: prompt}},
		Temperature: 0.7,
		MaxTokens:   c.maxTokens,
	}

	var response openAIChatResponse
	if err := c.sendRequest(ctx, "/chat/completions", request, &response); err != nil {
		return "", err
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no chat completion choices returned from OpenAI for model %s", c.modelName)
	}

	choice := response.Choices[0]
	if choice.Message.Content == "" {
		return "", fmt.Errorf("empty content from model %s despite normal completion. Finish reason: %s", c.modelName, choice.FinishReason)
	}
	return choice.Message.Content, nil
}

type openAIChatClient struct {
	openAIClient
}

func (c *openAIChatClient) Chat(ctx context.Context, messages []serverops.Message) (serverops.Message, error) {
	request := openAIChatRequest{
		Model:       c.modelName,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   c.maxTokens,
	}

	var response openAIChatResponse
	if err := c.sendRequest(ctx, "/chat/completions", request, &response); err != nil {
		return serverops.Message{}, err
	}

	if len(response.Choices) == 0 {
		return serverops.Message{}, fmt.Errorf("no chat choices returned from OpenAI for model %s", c.modelName)
	}

	choice := response.Choices[0]
	if choice.Message.Content == "" {
		return serverops.Message{}, fmt.Errorf("empty content from model %s despite normal completion. Finish reason: %s", c.modelName, choice.FinishReason)
	}
	return choice.Message, nil
}

type openAIEmbedClient struct {
	openAIClient
}

func (c *openAIEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	request := openAIEmbedRequest{
		Model:          c.modelName,
		Input:          prompt,
		EncodingFormat: "float", // Request float output
	}

	var response openAIEmbedResponse
	if err := c.sendRequest(ctx, "/embeddings", request, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 || len(response.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding data returned from OpenAI for model %s", c.modelName)
	}
	return response.Data[0].Embedding, nil
}

type openAIStreamClient struct {
	openAIClient
}

func (c *openAIStreamClient) Stream(ctx context.Context, prompt string) (<-chan string, error) {
	return nil, fmt.Errorf("streaming not supported yet for OpenAI")
}

func (c *openAIClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	url := c.baseURL + endpoint

	var reqBody io.Reader
	if request != nil {
		marshaledReqBody, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(marshaledReqBody)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorResponse struct {
			Error struct {
				Message string      `json:"message"`
				Type    string      `json:"type"`
				Code    interface{} `json:"code"`
			} `json:"error"`
		}
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			if jsonErr := json.Unmarshal(bodyBytes, &errorResponse); jsonErr == nil && errorResponse.Error.Message != "" {
				return fmt.Errorf("OpenAI API returned non-200 status: %d, Type: %s, Code: %v, Message: %s for model %s", resp.StatusCode, errorResponse.Error.Type, errorResponse.Error.Code, errorResponse.Error.Message, c.modelName)
			}
			return fmt.Errorf("OpenAI API returned non-200 status: %d, body: %s for model %s", resp.StatusCode, string(bodyBytes), c.modelName)
		}
		return fmt.Errorf("OpenAI API returned non-200 status: %d for model %s", resp.StatusCode, c.modelName)
	}

	if response != nil {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
		}
	}

	return nil
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []serverops.Message `json:"messages"`
	Temperature float64             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	Choices []struct {
		Index        int               `json:"index"`
		Message      serverops.Message `json:"message"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIChatStreamResponseChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int               `json:"index"`
		Delta        serverops.Message `json:"delta"` // Delta contains partial content
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	// TODO: usage is only present in the last chunk when stream_options={"include_usage": true}
	// Usage *struct {
	// 	PromptTokens     int `json:"prompt_tokens"`
	// 	CompletionTokens int `json:"completion_tokens"`
	// 	TotalTokens      int `json:"total_tokens"`
	// } `json:"usage,omitempty"`
}

// Structures for Embeddings API
type openAIEmbedRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"` // "float" or "base64"
}

type openAIEmbedResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func readLine(r io.Reader) ([]byte, error) {
	var line []byte
	buf := make([]byte, 1) // Read byte by byte
	for {
		n, err := r.Read(buf)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			line = append(line, buf[0])
			if buf[0] == '\n' {
				return line, nil
			}
		}
	}
}
