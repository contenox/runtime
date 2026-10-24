package llmrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelrepo"
)

func (e *modelManager) NextEdit(ctx context.Context, req Request, input modelrepo.NextEditRequest) (modelrepo.NextEditResult, Meta, error) {
	if ctx == nil {
		return modelrepo.NextEditResult{}, Meta{}, errors.New("next edit context is required")
	}
	input.SessionKey = effectiveSessionKey(ctx, req)
	if err := input.Validate(); err != nil {
		return modelrepo.NextEditResult{}, Meta{}, err
	}
	resolverReq := e.convertToResolverRequest(req, nil)
	policy := llmresolver.StickyOrRandom(input.SessionKey)
	selections, err := llmresolver.NextEditSelections(ctx, resolverReq, e.GetRuntime(ctx), policy)
	if err != nil && e.reconcileForResolution(ctx, err, req.ProviderTypes...) {
		selections, err = llmresolver.NextEditSelections(ctx, resolverReq, e.GetRuntime(ctx), policy)
	}
	if err != nil {
		return modelrepo.NextEditResult{}, Meta{}, fmt.Errorf("next edit resolution: %w", err)
	}
	if len(selections) != 1 {
		return modelrepo.NextEditResult{}, Meta{}, errors.New("next edit requires an unambiguous connection; " + remedy.PickConnection)
	}
	sel := selections[0]
	if !e.backendAllowedByHealth(ctx, req, sel.Backend) {
		return modelrepo.NextEditResult{}, Meta{}, errors.New("next edit connection is unavailable; " + remedy.RepairConnection)
	}
	if err := e.enforceResolutionBounds(ctx, "next-edit", sel.Provider, sel.Backend); err != nil {
		return modelrepo.NextEditResult{}, Meta{}, err
	}
	client, err := sel.Provider.GetNextEditConnection(modelrepo.WithRequestedContextLength(ctx, req.ContextLength), sel.Backend)
	if err != nil {
		return modelrepo.NextEditResult{}, Meta{}, err
	}
	defer safeClose(client)
	result, err := client.NextEdit(ctx, input)
	if err != nil {
		return modelrepo.NextEditResult{}, Meta{}, fmt.Errorf("next edit execution: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return modelrepo.NextEditResult{}, Meta{}, err
	}
	meta := Meta{ModelName: sel.Provider.ModelName(), ProviderType: sel.Provider.GetType(), BackendID: sel.Backend, Usage: result.Usage}
	if result.Usage != nil {
		e.reportTokenUsage(ctx, req, meta, *result.Usage)
	}
	return result, meta, nil
}
