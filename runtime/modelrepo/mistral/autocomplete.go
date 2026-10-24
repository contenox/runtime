package mistral

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type autocompleteClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
	model   string
	tracker libtracker.ActivityTracker
}

type fimRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Suffix      string   `json:"suffix,omitempty"`
	MaxTokens   int      `json:"max_tokens"`
	MinTokens   int      `json:"min_tokens,omitempty"`
	Temperature float64  `json:"temperature"`
	TopP        float64  `json:"top_p"`
	RandomSeed  int      `json:"random_seed"`
	Stop        []string `json:"stop,omitempty"`
	Stream      bool     `json:"stream"`
}

type fimResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *autocompleteClient) Autocomplete(ctx context.Context, input modelrepo.AutocompleteRequest) (result modelrepo.AutocompleteResult, err error) {
	reportErr, _, end := c.tracker.Start(ctx, "autocomplete", c.model)
	defer end()
	defer func() {
		if err != nil {
			reportErr(err)
		}
	}()
	if err := input.Validate(); err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if input.Temperature > maxTemperature {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("mistral accepts a temperature of at most %v, got %v", maxTemperature, input.Temperature)
	}
	requestBody, err := json.Marshal(fimRequest{Model: c.model, Prompt: input.Prefix, Suffix: input.Suffix, MaxTokens: input.MaxTokens, MinTokens: input.MinTokens, Temperature: input.Temperature, TopP: input.TopP, RandomSeed: input.Seed, Stop: input.StopSequences})
	if err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("encode Mistral FIM request: %w", err)
	}
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/fim/completions", bytes.NewReader(requestBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		return req, nil
	})
	if err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("mistral FIM request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("read Mistral FIM response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		code, message := modelrepo.ProviderErrorDetail(body)
		return modelrepo.AutocompleteResult{}, modelrepo.ClassifyProviderError(
			fmt.Errorf("mistral FIM returned %d: %s", resp.StatusCode, message), resp.StatusCode, code, message)
	}
	var response fimResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("decode Mistral FIM response: %w", err)
	}
	if len(response.Choices) != 1 {
		return modelrepo.AutocompleteResult{}, errors.New("mistral FIM requires exactly one completion choice")
	}
	choice := response.Choices[0]
	out, err := fimOutcome(choice.FinishReason, choice.Message.Content)
	if err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if response.Usage != nil {
		out.Usage = &modelrepo.TokenUsage{PromptTokens: response.Usage.PromptTokens, CompletionTokens: response.Usage.CompletionTokens, TotalTokens: response.Usage.TotalTokens}
	}
	return out, nil
}

func fimOutcome(finishReason, content string) (modelrepo.AutocompleteResult, error) {
	switch finishReason {
	case "stop", "length":
		return modelrepo.AutocompleteResult{Text: content, FinishReason: finishReason}, nil
	case "model_length":
		return modelrepo.AutocompleteResult{}, fmt.Errorf("%w: mistral FIM reached the model's context length", modelrepo.ErrContextLengthExceeded)
	case "error":
		return modelrepo.AutocompleteResult{}, errors.New("mistral FIM reported the error finish reason")
	default:
		return modelrepo.AutocompleteResult{}, fmt.Errorf("mistral FIM did not complete: finish reason %q", finishReason)
	}
}
