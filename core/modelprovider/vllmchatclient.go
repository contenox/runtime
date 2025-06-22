package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/contenox/contenox/core/serverops"
)

// Base client configuration
type vLLMClient struct {
	baseURL    string
	httpClient *http.Client
	modelName  string
}

// VLLMPromptClient handles prompt execution
type VLLMPromptClient struct {
	vLLMClient
}

// VLLMChatClient handles chat interactions
type VLLMChatClient struct {
	vLLMClient
}

// NewVLLMPromptClient creates a new prompt client
func NewVLLMPromptClient(baseURL, modelName string, httpClient *http.Client) *VLLMPromptClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &VLLMPromptClient{
		vLLMClient: vLLMClient{
			baseURL:    baseURL,
			httpClient: httpClient,
			modelName:  modelName,
		},
	}
}

// NewVLLMChatClient creates a new chat client
func NewVLLMChatClient(baseURL, modelName string, httpClient *http.Client) *VLLMChatClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &VLLMChatClient{
		vLLMClient: vLLMClient{
			baseURL:    baseURL,
			httpClient: httpClient,
			modelName:  modelName,
		},
	}
}

// Prompt implements LLMPromptExecClient interface
func (c *VLLMPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	request := completionRequest{
		Model:  c.modelName,
		Prompt: prompt,
		// Temperature: 0.0,
		// MaxTokens:   4096,
	}

	var response completionResponse
	if err := c.sendRequest(ctx, "/completions", request, &response); err != nil {
		return "", err
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no completion choices returned from vLLM for model %s", c.modelName)
	}

	choice := response.Choices[0]
	switch choice.FinishReason {
	case "stop":
		if choice.Text == "" {
			return "", fmt.Errorf("empty content from model %s despite normal completion", c.modelName)
		}
		return choice.Text, nil
	case "length":
		return "", fmt.Errorf("token limit reached for model %s (partial response: %q)", c.modelName, choice.Text)
	case "content_filter":
		return "", fmt.Errorf("content filtered for model %s (partial response: %q)", c.modelName, choice.Text)
	default:
		return "", fmt.Errorf("unexpected completion reason %q for model %s", choice.FinishReason, c.modelName)
	}
}

// Chat implements LLMChatClient interface
func (c *VLLMChatClient) Chat(ctx context.Context, messages []serverops.Message) (serverops.Message, error) {
	request := chatRequest{
		Model:    c.modelName,
		Messages: messages,
		// Temperature: 0.0,
		// MaxTokens:   4096,
	}

	var response chatResponse
	if err := c.sendRequest(ctx, "/chat/completions", request, &response); err != nil {
		return serverops.Message{}, err
	}

	if len(response.Choices) == 0 {
		return serverops.Message{}, fmt.Errorf("no chat choices returned from vLLM for model %s", c.modelName)
	}

	choice := response.Choices[0]
	switch choice.FinishReason {
	case "stop":
		if choice.Message.Content == "" {
			return serverops.Message{}, fmt.Errorf("empty content from model %s despite normal completion", c.modelName)
		}
		return choice.Message, nil
	case "length":
		return serverops.Message{}, fmt.Errorf(
			"token limit reached for model %s (partial response: %q)",
			c.modelName,
			choice.Message.Content,
		)
	case "content_filter":
		return serverops.Message{}, fmt.Errorf(
			"content filtered for model %s (partial response: %q)",
			c.modelName,
			choice.Message.Content,
		)
	default:
		return serverops.Message{}, fmt.Errorf(
			"unexpected completion reason %q for model %s",
			choice.FinishReason,
			c.modelName,
		)
	}
}

// Shared request handling method
func (c *vLLMClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	url := c.baseURL + endpoint

	reqBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vLLM API returned non-200 status: %d for model %s", resp.StatusCode, c.modelName)
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		return fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
	}

	return nil
}

// Request/response structures for vLLM API
type completionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

type completionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Text         string `json:"text"`
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type chatRequest struct {
	Model       string              `json:"model"`
	Messages    []serverops.Message `json:"messages"`
	Temperature float64             `json:"temperature"`
	MaxTokens   int                 `json:"max_tokens"`
}

type chatResponse struct {
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

// Interface implementation guarantees
var (
	_ serverops.LLMPromptExecClient = (*VLLMPromptClient)(nil)
	_ serverops.LLMChatClient       = (*VLLMChatClient)(nil)
)
