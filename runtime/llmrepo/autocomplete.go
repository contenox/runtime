package llmrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelrepo"
)

func (e *modelManager) Autocomplete(ctx context.Context, req Request, input modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, Meta, error) {
	if ctx == nil {
		return modelrepo.AutocompleteResult{}, Meta{}, errors.New("autocomplete context is required")
	}
	input.SessionKey = effectiveSessionKey(ctx, req)
	if err := input.Validate(); err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, err
	}
	resolverReq := e.convertToResolverRequest(req, nil)
	policy := llmresolver.StickyOrRandom(input.SessionKey)
	selections, err := llmresolver.AutocompleteSelections(ctx, resolverReq, e.GetRuntime(ctx), policy)
	if err != nil && e.reconcileForResolution(ctx, err, req.ProviderTypes...) {
		selections, err = llmresolver.AutocompleteSelections(ctx, resolverReq, e.GetRuntime(ctx), policy)
	}
	if err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, fmt.Errorf("autocomplete resolution: %w", err)
	}
	if len(selections) != 1 {
		return modelrepo.AutocompleteResult{}, Meta{}, errors.New("autocomplete requires an unambiguous connection; " + remedy.PickConnection)
	}
	sel := selections[0]
	if !e.backendAllowedByHealth(ctx, req, sel.Backend) {
		return modelrepo.AutocompleteResult{}, Meta{}, errors.New("autocomplete connection is unavailable; " + remedy.RepairConnection)
	}
	if err := e.enforceResolutionBounds(ctx, "autocomplete", sel.Provider, sel.Backend); err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, err
	}
	client, err := sel.Provider.GetAutocompleteConnection(modelrepo.WithRequestedContextLength(ctx, req.ContextLength), sel.Backend)
	if err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, err
	}
	defer safeClose(client)
	result, err := client.Autocomplete(ctx, input)
	if err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, fmt.Errorf("autocomplete execution: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return modelrepo.AutocompleteResult{}, Meta{}, err
	}
	meta := Meta{ModelName: sel.Provider.ModelName(), ProviderType: sel.Provider.GetType(), BackendID: sel.Backend, Usage: result.Usage}
	if result.Usage != nil {
		e.reportTokenUsage(ctx, req, meta, *result.Usage)
	}
	return result, meta, nil
}
