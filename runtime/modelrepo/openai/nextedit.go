package openai

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/completions"
	"github.com/contenox/runtime/runtime/modelrepo/codec/nextedit"
)

type nextEditClient struct {
	openAIClient
	format string
}

func (p *OpenAIProvider) GetNextEditConnection(_ context.Context, backend string) (modelrepo.LLMNextEditClient, error) {
	if !p.CanNextEdit() {
		return nil, modelrepo.ErrNextEditUnsupported
	}
	return &nextEditClient{openAIClient: openAIClient{baseURL: backend, httpClient: p.httpClient, modelName: p.modelName, maxOutputTokens: p.maxOutputTokens, apiKey: p.apiKey, tracker: p.tracker}, format: p.nextEditFormat}, nil
}

func (p *nextEditClient) NextEdit(ctx context.Context, input modelrepo.NextEditRequest) (modelrepo.NextEditResult, error) {
	if err := input.Validate(); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	if p.maxOutputTokens > 0 && input.MaxTokens > p.maxOutputTokens {
		return modelrepo.NextEditResult{}, fmt.Errorf("next edit output limit exceeds the model's declared maximum of %d tokens", p.maxOutputTokens)
	}
	if err := nextedit.ValidateLimit(p.format, input.MaxTokens); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	prepared, err := nextedit.Prepare(p.format, input.Input)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	request := completions.Request{Model: p.modelName, Prompt: prepared.Render(), MaxTokens: input.MaxTokens, Temperature: input.Temperature, TopP: input.TopP, Seed: input.Seed, Stop: prepared.EndpointStops()}
	var response completions.Response
	if err := p.sendRequest(ctx, "/completions", request, &response); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	completion, err := response.Result()
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	edits, err := prepared.Parse(completion.Text, completion.FinishReason)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	return modelrepo.NextEditResult{Edits: edits, FinishReason: completion.FinishReason, Usage: completion.Usage}, nil
}
