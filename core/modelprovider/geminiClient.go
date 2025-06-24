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

type geminiClient struct {
	apiKey     string
	modelName  string
	baseURL    string
	httpClient *http.Client
	maxTokens  int // Max output tokens, or context length for models like embeddings
}

type geminiPromptClient struct {
	geminiClient
}

func (c *geminiPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	// Convert the single prompt string into a Gemini-style message array
	geminiMessages := []geminiContent{
		{
			Role:  "user",
			Parts: []geminiPart{{Text: prompt}},
		},
	}

	request := geminiGenerateContentRequest{
		Contents: geminiMessages,
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     0.7, // Default temperature
			MaxOutputTokens: c.maxTokens,
		},
	}

	endpoint := fmt.Sprintf("/models/%s:generateContent", c.modelName)
	var response geminiGenerateContentResponse
	if err := c.sendRequest(ctx, endpoint, request, &response); err != nil {
		return "", err
	}

	if len(response.Candidates) == 0 {
		return "", fmt.Errorf("no candidates returned from Gemini for model %s", c.modelName)
	}

	candidate := response.Candidates[0]
	if len(candidate.Content.Parts) == 0 || candidate.Content.Parts[0].Text == "" {
		// Check for specific finish reasons for more informative errors
		if len(candidate.FinishReason) > 0 {
			return "", fmt.Errorf("empty content from model %s despite normal completion. Finish reason: %s", c.modelName, candidate.FinishReason[0])
		}
		return "", fmt.Errorf("empty content from model %s", c.modelName)
	}
	return candidate.Content.Parts[0].Text, nil
}

type geminiChatClient struct {
	geminiClient
}

func (c *geminiChatClient) Chat(ctx context.Context, messages []serverops.Message) (serverops.Message, error) {
	geminiMessages := convertToGeminiMessages(messages)

	request := geminiGenerateContentRequest{
		Contents: geminiMessages,
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     0.7, // Default temperature
			MaxOutputTokens: c.maxTokens,
		},
	}

	endpoint := fmt.Sprintf("/models/%s:generateContent", c.modelName)
	var response geminiGenerateContentResponse
	if err := c.sendRequest(ctx, endpoint, request, &response); err != nil {
		return serverops.Message{}, err
	}

	if len(response.Candidates) == 0 {
		return serverops.Message{}, fmt.Errorf("no candidates returned from Gemini for model %s", c.modelName)
	}

	candidate := response.Candidates[0]
	if len(candidate.Content.Parts) == 0 || candidate.Content.Parts[0].Text == "" {
		if len(candidate.FinishReason) > 0 {
			return serverops.Message{}, fmt.Errorf("empty content from model %s despite normal completion. Finish reason: %s", c.modelName, candidate.FinishReason[0])
		}
		return serverops.Message{}, fmt.Errorf("empty content from model %s", c.modelName)
	}

	return serverops.Message{Role: "model", Content: candidate.Content.Parts[0].Text}, nil
}

// geminiEmbedClient implements serverops.LLMEmbedClient
type geminiEmbedClient struct {
	geminiClient
}

func (c *geminiEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	request := geminiEmbedContentRequest{
		Model: "models/" + c.modelName, // Gemini requires model in request body for embedContent
		Content: geminiContent{
			Parts: []geminiPart{{Text: prompt}},
		},
	}

	endpoint := fmt.Sprintf("/models/%s:embedContent", c.modelName)
	var response geminiEmbedContentResponse
	if err := c.sendRequest(ctx, endpoint, request, &response); err != nil {
		return nil, err
	}

	if len(response.Embedding.Values) == 0 {
		return nil, fmt.Errorf("no embedding values returned from Gemini for model %s", c.modelName)
	}
	return response.Embedding.Values, nil
}

type geminiStreamClient struct {
	geminiClient
}

func (c *geminiStreamClient) Stream(ctx context.Context, prompt string) (<-chan string, error) {
	geminiMessages := []geminiContent{
		{
			Role:  "user",
			Parts: []geminiPart{{Text: prompt}},
		},
	}

	request := geminiGenerateContentRequest{
		Contents: geminiMessages,
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     0.7,
			MaxOutputTokens: c.maxTokens,
		},
	}

	endpoint := fmt.Sprintf("/models/%s:streamGenerateContent", c.modelName)
	fullURL := fmt.Sprintf("%s%s?key=%s", c.baseURL, endpoint, c.apiKey)

	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request for streaming: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create streaming request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed for streaming for model %s: %w", c.modelName, err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini API streaming returned non-200 status: %d, body: %s for model %s", resp.StatusCode, string(bodyBytes), c.modelName)
	}

	output := make(chan string)
	go func() {
		defer resp.Body.Close()
		defer close(output)

		decoder := json.NewDecoder(resp.Body)
		for {
			select {
			case <-ctx.Done():
				return // Context cancelled, stop streaming
			default:
				var chunk geminiGenerateContentResponse
				if err := decoder.Decode(&chunk); err != nil {
					if err == io.EOF {
						return // Stream finished
					}
					// TODO: Log other errors, but try to continue if possible or break
					return
				}

				if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 && chunk.Candidates[0].Content.Parts[0].Text != "" {
					output <- chunk.Candidates[0].Content.Parts[0].Text
				}
			}
		}
	}()

	return output, nil
}

func (c *geminiClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	fullURL := fmt.Sprintf("%s%s?key=%s", c.baseURL, endpoint, c.apiKey)

	var reqBody io.Reader
	if request != nil {
		marshaledReqBody, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(marshaledReqBody)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, reqBody)
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
		var errorResponse struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			if jsonErr := json.Unmarshal(bodyBytes, &errorResponse); jsonErr == nil && errorResponse.Error.Message != "" {
				return fmt.Errorf("gemini API returned non-200 status: %d, Status: %s, Message: %s for model %s", resp.StatusCode, errorResponse.Error.Status, errorResponse.Error.Message, c.modelName)
			}
			return fmt.Errorf("gemini API returned non-200 status: %d, body: %s for model %s", resp.StatusCode, string(bodyBytes), c.modelName)
		}
		return fmt.Errorf("gemini API returned non-200 status: %d for model %s", resp.StatusCode, c.modelName)
	}

	if response != nil {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
		}
	}

	return nil
}
