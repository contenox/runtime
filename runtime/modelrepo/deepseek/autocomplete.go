package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

const (
	maxFIMTokens      = 4096
	maxFIMStopPhrases = 16
)

const betaPathSegment = "beta"

func betaBaseURL(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return defaultBaseURL + "/" + betaPathSegment
	}
	if strings.HasSuffix(trimmed, "/"+betaPathSegment) {
		return trimmed
	}
	return strings.TrimSuffix(trimmed, "/v1") + "/" + betaPathSegment
}

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
	Temperature float64  `json:"temperature"`
	TopP        float64  `json:"top_p"`
	Stop        []string `json:"stop,omitempty"`
	Stream      bool     `json:"stream"`
}

type fimResponse struct {
	Choices []struct {
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
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
	if input.MaxTokens > maxFIMTokens {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("DeepSeek FIM output limit exceeds the documented maximum of %d tokens", maxFIMTokens)
	}
	if len(input.StopSequences) > maxFIMStopPhrases {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("DeepSeek FIM accepts at most %d stop sequences, got %d", maxFIMStopPhrases, len(input.StopSequences))
	}
	if input.MinTokens > 0 {
		return modelrepo.AutocompleteResult{}, errors.New("DeepSeek FIM declares no minimum-token member")
	}
	body, err := json.Marshal(fimRequest{Model: c.model, Prompt: input.Prefix, Suffix: input.Suffix, MaxTokens: input.MaxTokens, Temperature: input.Temperature, TopP: input.TopP, Stop: input.StopSequences})
	if err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("encode DeepSeek FIM request: %w", err)
	}
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/completions", bytes.NewReader(body))
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
		return modelrepo.AutocompleteResult{}, fmt.Errorf("DeepSeek FIM request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("read DeepSeek FIM response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		code, message := modelrepo.ProviderErrorDetail(responseBody)
		return modelrepo.AutocompleteResult{}, modelrepo.ClassifyProviderError(
			fmt.Errorf("DeepSeek FIM returned %d: %s", resp.StatusCode, message), resp.StatusCode, code, message)
	}
	var response fimResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("decode DeepSeek FIM response: %w", err)
	}
	if len(response.Choices) != 1 {
		return modelrepo.AutocompleteResult{}, errors.New("DeepSeek FIM requires exactly one completion choice")
	}
	choice := response.Choices[0]
	result, err = fimOutcome(choice.FinishReason, choice.Text)
	if err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if response.Usage != nil {
		result.Usage = &modelrepo.TokenUsage{PromptTokens: response.Usage.PromptTokens, CompletionTokens: response.Usage.CompletionTokens, TotalTokens: response.Usage.TotalTokens, CacheReadTokens: response.Usage.PromptCacheHitTokens}
	}
	return result, nil
}

func fimOutcome(finishReason, text string) (modelrepo.AutocompleteResult, error) {
	switch finishReason {
	case "stop", "length":
		return modelrepo.AutocompleteResult{Text: text, FinishReason: finishReason}, nil
	case "content_filter":
		return modelrepo.AutocompleteResult{}, &modelrepo.RefusalError{
			Provider: "deepseek",
			Code:     finishReason,
			Text:     "the completion was omitted by the provider's content filter",
		}
	case "insufficient_system_resource":
		return modelrepo.AutocompleteResult{}, fmt.Errorf("%w: DeepSeek FIM was interrupted for lack of inference capacity", modelrepo.ErrRateLimited)
	case "aborted":
		return modelrepo.AutocompleteResult{}, errors.New("DeepSeek FIM generation was aborted by the provider")
	default:
		return modelrepo.AutocompleteResult{}, fmt.Errorf("DeepSeek FIM did not complete: finish reason %q", finishReason)
	}
}
