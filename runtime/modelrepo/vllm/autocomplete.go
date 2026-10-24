package vllm

import (
	"context"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/completions"
)

type autocompleteClient struct {
	vLLMClient
	format string
}

func (p *vLLMProvider) GetAutocompleteConnection(_ context.Context, backend string) (modelrepo.LLMAutocompleteClient, error) {
	if !p.CanAutocomplete() {
		return nil, modelrepo.ErrAutocompleteUnsupported
	}
	if err := p.validateBackend(backend); err != nil {
		return nil, err
	}
	client := p.client
	if client == nil {
		client = modelrepo.SharedHTTPClient
	}
	return &autocompleteClient{vLLMClient: vLLMClient{baseURL: backend, httpClient: client, modelName: p.Name, maxOutputTokens: p.MaxOutputTokens, apiKey: p.authToken, tracker: p.tracker}, format: p.FIMFormat}, nil
}

func (c *autocompleteClient) Autocomplete(ctx context.Context, input modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, error) {
	request, err := completions.NewRequest(c.modelName, c.format, c.maxOutputTokens, input)
	if err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	var response completions.Response
	if err := c.sendRequest(ctx, "/v1/completions", request, &response); err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	return response.Result()
}
