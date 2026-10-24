package ollama

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/nextedit"
)

type nextEditClient struct {
	client    *ollamaHTTPClient
	model     string
	format    string
	maxTokens int
}

func (p *OllamaProvider) CanNextEdit() bool {
	if !p.SupportsNextEdit {
		return false
	}
	_, err := modelcontract.ResolveNextEdit(p.NextEditFormat)
	return err == nil
}

func (p *OllamaProvider) GetNextEditConnection(_ context.Context, backend string) (modelrepo.LLMNextEditClient, error) {
	if !p.CanNextEdit() {
		return nil, modelrepo.ErrNextEditUnsupported
	}
	client, err := newOllamaHTTPClient(backend, p.apiKey, p.httpClient)
	if err != nil {
		return nil, err
	}
	return &nextEditClient{client: client, model: p.Name, format: p.NextEditFormat, maxTokens: p.MaxOutputTokens}, nil
}

func (c *nextEditClient) NextEdit(ctx context.Context, input modelrepo.NextEditRequest) (modelrepo.NextEditResult, error) {
	if err := input.Validate(); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	if c.maxTokens > 0 && input.MaxTokens > c.maxTokens {
		return modelrepo.NextEditResult{}, fmt.Errorf("next edit output limit exceeds the model's declared maximum of %d tokens", c.maxTokens)
	}
	if err := nextedit.ValidateLimit(c.format, input.MaxTokens); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	prepared, err := nextedit.Prepare(c.format, input.Input)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	stream, truncate := false, false
	request := &GenerateRequest{Model: c.model, Prompt: prepared.Render(), Raw: true, Stream: &stream, Truncate: &truncate, KeepAlive: keepAlive(), Options: map[string]any{"num_predict": input.MaxTokens, "temperature": input.Temperature, "top_p": input.TopP, "seed": input.Seed, "stop": prepared.EndpointStops()}}
	var response GenerateResponse
	if err := c.client.Generate(ctx, request, func(value GenerateResponse) error { response = value; return nil }); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	if !response.Done || response.DoneReason != "stop" && response.DoneReason != "length" {
		return modelrepo.NextEditResult{}, errors.New("ollama next edit ended without a successful terminal response")
	}
	edits, err := prepared.Parse(response.Response, response.DoneReason)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	usage := &modelrepo.TokenUsage{PromptTokens: response.PromptEvalCount, CompletionTokens: response.EvalCount, TotalTokens: response.PromptEvalCount + response.EvalCount}
	return modelrepo.NextEditResult{Edits: edits, FinishReason: response.DoneReason, Usage: usage}, nil
}
