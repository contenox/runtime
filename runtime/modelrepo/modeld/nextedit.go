package modeld

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type nextEditClient struct {
	provider *ModeldProvider
	backend  string
}

// GetNextEditConnection binds the declared edit operation to its native endpoint.
func (p *ModeldProvider) GetNextEditConnection(_ context.Context, backend string) (modelrepo.LLMNextEditClient, error) {
	if !p.CanNextEdit() {
		return nil, modelrepo.ErrNextEditUnsupported
	}
	if _, err := modelcontract.ResolveNextEdit(p.NextEditFormat); err != nil {
		return nil, err
	}
	return &nextEditClient{provider: p, backend: backend}, nil
}

func (c *nextEditClient) NextEdit(ctx context.Context, input modelrepo.NextEditRequest) (modelrepo.NextEditResult, error) {
	if err := input.Validate(); err != nil {
		return modelrepo.NextEditResult{}, err
	}
	p := c.provider
	profile, err := modelcontract.ResolveNextEdit(p.NextEditFormat)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	if input.MaxTokens > profile.MaxTokens || p.MaxOutputTokens > 0 && input.MaxTokens > p.MaxOutputTokens {
		return modelrepo.NextEditResult{}, fmt.Errorf("next edit output limit exceeds the declared model contract")
	}
	prepared, err := modelcontract.PrepareNextEdit(p.NextEditFormat, input.Input)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	fingerprint, err := modelcontract.NextEditFingerprint(p.NextEditFormat)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	manifest, err := contextasm.BuildSplitManifest(prepared.Render(), "", nil, contextasm.ManifestIdentity{ProfileID: "next-edit:" + p.NextEditFormat, ModelDigest: p.artifact.Digest, PromptFormat: p.NextEditFormat, PromptTemplateDigest: fingerprint})
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	result, err := p.generateCode(ctx, c.backend, transport.Config{Operation: "next-edit", SessionNamespace: input.SessionKey}, transport.PrefixInput{Manifest: manifest, NextEdit: &input.Input, NextEditFormat: p.NextEditFormat}, transport.DecodeConfig{NextEditFormat: p.NextEditFormat, MaxTokens: input.MaxTokens, Temperature: &input.Temperature, TopP: &input.TopP, Seed: &input.Seed})
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	edits, err := prepared.Parse(result.Text, result.FinishReason)
	if err != nil {
		return modelrepo.NextEditResult{}, err
	}
	return modelrepo.NextEditResult{Edits: edits, FinishReason: result.FinishReason, Usage: result.Usage}, nil
}
