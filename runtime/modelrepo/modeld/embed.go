package modeld

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

// ModeldEmbedClient obtains vectors from a native daemon.
type ModeldEmbedClient struct {
	artifact  modeldconn.ModelRef
	modelName string
	backendID string
	tracker   libtracker.ActivityTracker
}

func (c *ModeldEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	reportErr, _, end := c.tracker.Start(ctx, "embed", "modeld", "model", c.modelName)
	defer end()

	target := resolveTarget(c.backendID)
	tConfig := transport.Config{}
	descriptor, err := modelregistry.NewCurated().Resolve(ctx, c.modelName)
	if err != nil {
		return nil, fmt.Errorf("embedding model requires an explicit retrieval contract: %w", err)
	}
	if !descriptor.SupportsRetrieval() {
		return nil, fmt.Errorf("model %s has no retrieval embedding contract; "+remedy.EmbeddingRole, c.modelName)
	}
	tConfig.EmbeddingPooling = descriptor.Embedding.Pooling
	tConfig.NumCtx = descriptor.Embedding.ContextTokens
	switch modelrepo.EmbeddingPurposeFromContext(ctx) {
	case modelrepo.EmbeddingDocument:
		prompt = descriptor.Embedding.DocumentPrefix + prompt
	case modelrepo.EmbeddingQuery:
		prompt = descriptor.Embedding.QueryPrefix + prompt
	default:
		return nil, fmt.Errorf("unknown embedding input purpose %q", modelrepo.EmbeddingPurposeFromContext(ctx))
	}

	res, err := embedWithAutoPull(ctx, target, c.modelName, c.artifact, tConfig, prompt, c.tracker)
	if err != nil {
		reportErr(err)
		return nil, fmt.Errorf("modeld embed for %s: %w", c.modelName, err)
	}
	if len(res.Vector) != descriptor.Embedding.Dimension {
		return nil, fmt.Errorf("model %s returned embedding dimension %d; its retrieval contract requires %d", c.modelName, len(res.Vector), descriptor.Embedding.Dimension)
	}

	out := make([]float64, len(res.Vector))
	for i, v := range res.Vector {
		out[i] = float64(v)
	}
	return out, nil
}

var _ modelrepo.LLMEmbedClient = (*ModeldEmbedClient)(nil)
