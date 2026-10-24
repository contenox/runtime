package openai

import (
	"context"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/completions"
)

type autocompleteClient struct {
	openAIClient
	format string
}

func (p *OpenAIProvider) GetAutocompleteConnection(_ context.Context, backend string) (modelrepo.LLMAutocompleteClient, error) {
	if !p.CanAutocomplete() {
		return nil, modelrepo.ErrAutocompleteUnsupported
	}
	return &autocompleteClient{openAIClient: openAIClient{baseURL: backend, httpClient: p.httpClient, modelName: p.modelName, maxOutputTokens: p.maxOutputTokens, apiKey: p.apiKey, tracker: p.tracker}, format: p.fimFormat}, nil
}

func (c *autocompleteClient) Autocomplete(ctx context.Context, input modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, error) {
	request, err := completions.NewRequest(c.modelName, c.format, c.maxOutputTokens, input)
	if err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	var response completions.Response
	if err := c.sendRequest(ctx, "/completions", request, &response); err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	return response.Result()
}
