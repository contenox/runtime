package libmodelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OpenAIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	modelName  string
	maxTokens  int
}

type OpenAIPromptClient struct {
	OpenAIClient
}

type OpenAIChatClient struct {
	OpenAIClient
}

func NewOpenAIPromptClient(ctx context.Context, apiKey, modelName string, httpClient *http.Client) (*OpenAIPromptClient, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	client := &OpenAIPromptClient{
		OpenAIClient: OpenAIClient{
			baseURL:    "https://api.openai.com/v1",
			apiKey:     apiKey,
			httpClient: httpClient,
			modelName:  modelName,
		},
	}
	client.maxTokens = 2048
	return client, nil
}

// NewOpenAIChatClient creates a new chat client for OpenAI
func NewOpenAIChatClient(ctx context.Context, apiKey, modelName string, httpClient *http.Client) (*OpenAIChatClient, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	client := &OpenAIChatClient{
		OpenAIClient: OpenAIClient{
			baseURL:    "https://api.openai.com/v1",
			apiKey:     apiKey,
			httpClient: httpClient,
			modelName:  modelName,
		},
	}

	client.maxTokens = 2048
	return client, nil
}

func (c *OpenAIPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	request := openAIChatRequest{
		Model:       c.modelName,
		Messages:    []Message{{Role: "user", Content: prompt}},
		Temperature: 0.5,
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
	switch choice.FinishReason {
	case "stop":
		if choice.Message.Content == "" {
			return "", fmt.Errorf("empty content from model %s despite normal completion", c.modelName)
		}
		return choice.Message.Content, nil
	case "length":
		return "", fmt.Errorf("token limit reached for model %s (partial response: %q)", c.modelName, choice.Message.Content)
	case "content_filter":
		return "", fmt.Errorf("content filtered for model %s (partial response: %q)", c.modelName, choice.Message.Content)
	default:
		return "", fmt.Errorf("unexpected completion reason %q for model %s", choice.FinishReason, c.modelName)
	}
}

func (c *OpenAIChatClient) Chat(ctx context.Context, messages []Message) (Message, error) {
	request := openAIChatRequest{
		Model:       c.modelName,
		Messages:    messages,
		Temperature: 0.5,
		MaxTokens:   c.maxTokens,
	}

	var response openAIChatResponse
	if err := c.sendRequest(ctx, "/chat/completions", request, &response); err != nil {
		return Message{}, err
	}

	if len(response.Choices) == 0 {
		return Message{}, fmt.Errorf("no chat choices returned from OpenAI for model %s", c.modelName)
	}

	choice := response.Choices[0]
	switch choice.FinishReason {
	case "stop":
		if choice.Message.Content == "" {
			return Message{}, fmt.Errorf("empty content from model %s despite normal completion", c.modelName)
		}
		return choice.Message, nil
	case "length":
		return Message{}, fmt.Errorf(
			"token limit reached for model %s (partial response: %q)",
			c.modelName,
			choice.Message.Content,
		)
	case "content_filter":
		return Message{}, fmt.Errorf(
			"content filtered for model %s (partial response: %q)",
			c.modelName,
			choice.Message.Content,
		)
	default:
		return Message{}, fmt.Errorf(
			"unexpected completion reason %q for model %s",
			choice.FinishReason,
			c.modelName,
		)
	}
}

func (c *OpenAIClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
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
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errorResponse); err == nil && errorResponse.Error.Message != "" {
			return fmt.Errorf("OpenAI API returned non-200 status: %d, Type: %s, Message: %s for model %s", resp.StatusCode, errorResponse.Error.Type, errorResponse.Error.Message, c.modelName)
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

var (
	_ LLMPromptExecClient = (*OpenAIPromptClient)(nil)
	_ LLMChatClient       = (*OpenAIChatClient)(nil)
)
