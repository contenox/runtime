package completions

import (
	"errors"
	"fmt"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type Request struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature float64  `json:"temperature"`
	TopP        float64  `json:"top_p"`
	Seed        int      `json:"seed"`
	Stop        []string `json:"stop"`
	Stream      bool     `json:"stream"`
	Echo        bool     `json:"echo"`
}

func NewRequest(model, format string, limit int, input modelrepo.AutocompleteRequest) (Request, error) {
	if err := input.Validate(); err != nil {
		return Request{}, err
	}
	if limit > 0 && input.MaxTokens > limit {
		return Request{}, fmt.Errorf("autocomplete output limit exceeds the model's declared maximum of %d tokens", limit)
	}
	contract, err := modelcontract.ResolveFIM(format)
	if err != nil {
		return Request{}, err
	}
	if contract.Kind != "tokens" {
		return Request{}, fmt.Errorf("FIM contract %q requires its provider endpoint", format)
	}
	return Request{Model: model, Prompt: contract.Render(input.Prefix, input.Suffix), MaxTokens: input.MaxTokens, Temperature: input.Temperature, TopP: input.TopP, Seed: input.Seed, Stop: contract.Stops}, nil
}

type Response struct {
	Choices []struct {
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptDetails    struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func (r Response) Result() (modelrepo.AutocompleteResult, error) {
	if len(r.Choices) != 1 {
		return modelrepo.AutocompleteResult{}, errors.New("autocomplete requires exactly one completion choice")
	}
	choice := r.Choices[0]
	if choice.FinishReason != "stop" && choice.FinishReason != "length" {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("autocomplete did not complete: finish reason %q", choice.FinishReason)
	}
	result := modelrepo.AutocompleteResult{Text: choice.Text, FinishReason: choice.FinishReason}
	if r.Usage != nil {
		result.Usage = &modelrepo.TokenUsage{PromptTokens: r.Usage.PromptTokens, CompletionTokens: r.Usage.CompletionTokens, TotalTokens: r.Usage.TotalTokens, CacheReadTokens: r.Usage.PromptDetails.CachedTokens}
	}
	return result, nil
}
