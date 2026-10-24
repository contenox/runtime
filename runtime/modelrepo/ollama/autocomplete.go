package ollama

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
)

const OllamaInsertFIMFormat = "ollama-insert"

type autocompleteClient struct {
	client    *ollamaHTTPClient
	model     string
	format    string
	maxTokens int
	insert    bool
}

func (p *OllamaProvider) GetAutocompleteConnection(_ context.Context, backend string) (modelrepo.LLMAutocompleteClient, error) {
	if !p.CanAutocomplete() {
		return nil, modelrepo.ErrAutocompleteUnsupported
	}
	client, err := newOllamaHTTPClient(backend, p.apiKey, p.httpClient)
	if err != nil {
		return nil, err
	}
	return &autocompleteClient{
		client:    client,
		model:     p.Name,
		format:    p.FIMFormat,
		maxTokens: p.MaxOutputTokens,
		insert:    strings.TrimSpace(p.FIMFormat) == "" || p.FIMFormat == OllamaInsertFIMFormat,
	}, nil
}

func (c *autocompleteClient) Autocomplete(ctx context.Context, input modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, error) {
	if err := input.Validate(); err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if c.maxTokens > 0 && input.MaxTokens > c.maxTokens {
		return modelrepo.AutocompleteResult{}, fmt.Errorf("autocomplete output limit exceeds the model's declared maximum of %d tokens", c.maxTokens)
	}

	stream, truncate := false, false
	options := map[string]any{
		"num_predict": input.MaxTokens,
		"temperature": input.Temperature,
		"top_p":       input.TopP,
		"seed":        input.Seed,
	}
	request := &GenerateRequest{
		Model:     c.model,
		Stream:    &stream,
		Truncate:  &truncate,
		KeepAlive: keepAlive(),
		Options:   options,
	}

	if c.insert {
		request.Prompt = input.Prefix
		request.Suffix = input.Suffix
	} else {
		format, err := modelcontract.ResolveFIM(c.format)
		if err != nil {
			return modelrepo.AutocompleteResult{}, err
		}
		if format.Kind != "tokens" {
			return modelrepo.AutocompleteResult{}, modelrepo.ErrAutocompleteUnsupported
		}
		request.Prompt = format.Render(input.Prefix, input.Suffix)
		request.Raw = true
		options["stop"] = format.Stops
	}

	var response GenerateResponse
	err := c.client.Generate(ctx, request, func(value GenerateResponse) error { response = value; return nil })
	if err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if !response.Done || response.DoneReason != "stop" && response.DoneReason != "length" {
		return modelrepo.AutocompleteResult{}, errors.New("ollama autocomplete ended without a successful terminal response")
	}
	return modelrepo.AutocompleteResult{Text: response.Response, FinishReason: response.DoneReason, Usage: response.Metrics.Usage()}, nil
}

var _ modelrepo.LLMAutocompleteClient = (*autocompleteClient)(nil)
