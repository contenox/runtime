package modelrepo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

var ErrAutocompleteUnsupported = errors.New("native autocomplete is unavailable; select a FIM-capable connection and model or explicitly configure instructed completion")

type AutocompleteRequest struct {
	Prefix        string
	Suffix        string
	MaxTokens     int
	Temperature   float64
	TopP          float64
	Seed          int
	SessionKey    string
	StopSequences []string
	MinTokens     int
}

func (r AutocompleteRequest) Validate() error {
	if r.Prefix == "" && r.Suffix == "" {
		return errors.New("autocomplete requires a prefix or suffix")
	}
	if !utf8.ValidString(r.Prefix) || !utf8.ValidString(r.Suffix) {
		return errors.New("autocomplete source must be valid UTF-8")
	}
	if r.MaxTokens <= 0 || math.IsNaN(r.Temperature) || math.IsInf(r.Temperature, 0) || r.Temperature < 0 || math.IsNaN(r.TopP) || math.IsInf(r.TopP, 0) || r.TopP <= 0 || r.TopP > 1 || r.Seed < 0 || r.SessionKey == "" {
		return errors.New("autocomplete requires a positive token limit, valid sampling settings and session identity")
	}
	if r.MinTokens < 0 {
		return fmt.Errorf("autocomplete minimum tokens cannot be negative, got %d", r.MinTokens)
	}
	if r.MinTokens > r.MaxTokens {
		return fmt.Errorf("autocomplete minimum tokens %d exceeds the maximum %d", r.MinTokens, r.MaxTokens)
	}
	for _, stop := range r.StopSequences {
		if stop == "" {
			return errors.New("an autocomplete stop sequence must not be empty")
		}
	}
	return nil
}

type AutocompleteResult struct {
	Text         string
	FinishReason string
	Usage        *TokenUsage
}

type LLMAutocompleteClient interface {
	Autocomplete(context.Context, AutocompleteRequest) (AutocompleteResult, error)
}
