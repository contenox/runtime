package modeld

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type autocompleteClient struct {
	provider *ModeldProvider
	backend  string
}

// GetAutocompleteConnection binds native FIM to this model and endpoint.
func (p *ModeldProvider) GetAutocompleteConnection(_ context.Context, backend string) (modelrepo.LLMAutocompleteClient, error) {
	if !p.CanAutocomplete() {
		return nil, modelrepo.ErrAutocompleteUnsupported
	}
	format, err := modelcontract.ResolveFIM(p.FIMFormat)
	if err != nil {
		return nil, err
	}
	if format.Kind != "tokens" {
		return nil, modelrepo.ErrAutocompleteUnsupported
	}
	return &autocompleteClient{provider: p, backend: backend}, nil
}

func (c *autocompleteClient) Autocomplete(ctx context.Context, input modelrepo.AutocompleteRequest) (result modelrepo.AutocompleteResult, resultErr error) {
	if err := input.Validate(); err != nil {
		return result, err
	}
	p := c.provider
	if p.MaxOutputTokens > 0 && input.MaxTokens > p.MaxOutputTokens {
		return result, fmt.Errorf("autocomplete output limit exceeds the model's declared maximum of %d tokens", p.MaxOutputTokens)
	}
	format, err := contextasm.ResolveFIM(p.FIMFormat)
	if err != nil {
		return result, err
	}
	prompt := format.Render(input.Prefix, input.Suffix)
	fingerprint, err := modelcontract.FIMFingerprint(p.FIMFormat)
	if err != nil {
		return result, err
	}
	manifest, err := contextasm.BuildSplitManifest(prompt, "", nil, contextasm.ManifestIdentity{ProfileID: "autocomplete:" + p.FIMFormat, ModelDigest: p.artifact.Digest, PromptFormat: p.FIMFormat, PromptTemplateDigest: fingerprint})
	if err != nil {
		return result, err
	}
	return p.generateCode(ctx, c.backend, transport.Config{Operation: "autocomplete", SessionNamespace: input.SessionKey}, transport.PrefixInput{Manifest: manifest, Autocomplete: &contextasm.AutocompleteInput{Format: p.FIMFormat, Prefix: input.Prefix, Suffix: input.Suffix}}, transport.DecodeConfig{AutocompleteFormat: p.FIMFormat, MaxTokens: input.MaxTokens, Temperature: &input.Temperature, TopP: &input.TopP, Seed: &input.Seed})
}

func (p *ModeldProvider) generateCode(ctx context.Context, backend string, config transport.Config, prefix transport.PrefixInput, decode transport.DecodeConfig) (result modelrepo.AutocompleteResult, resultErr error) {
	if ctx == nil {
		return result, errors.New("native code generation requires context")
	}
	session, err := openSessionWithAutoPull(ctx, resolveTarget(backend), p.Name, p.artifact, config, p.tracker)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, session.Close()) }()
	status, err := session.EnsurePrefix(ctx, prefix)
	if err != nil {
		return result, err
	}
	if status.AvailableTokens < decode.MaxTokens {
		return result, transport.NewContextOverflowError(config.Operation, status.ResidentTokens, decode.MaxTokens, status.ResidentTokens+status.AvailableTokens)
	}
	chunks, err := session.Decode(ctx, decode)
	if err != nil {
		return result, err
	}
	var text strings.Builder
	usage := &modelrepo.TokenUsage{PromptTokens: status.PrefixTokens, CacheReadTokens: status.ReusedTokens}
	for chunk := range chunks {
		if chunk.Error != nil {
			return result, chunk.Error
		}
		if chunk.Thinking != "" || len(chunk.ToolCalls) > 0 {
			return result, errors.New("native autocomplete returned chat output")
		}
		text.WriteString(chunk.Text)
		if chunk.Usage != nil {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
		}
		if chunk.FinishReason != "" {
			result.FinishReason = chunk.FinishReason
		}
	}
	if err := ctx.Err(); err != nil {
		return modelrepo.AutocompleteResult{}, err
	}
	if result.FinishReason == "" {
		return result, errors.New("native autocomplete ended without a terminal result")
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	result.Text, result.Usage = text.String(), usage
	return result, nil
}

var _ modelrepo.LLMAutocompleteClient = (*autocompleteClient)(nil)
