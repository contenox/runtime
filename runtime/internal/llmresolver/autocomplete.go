package llmresolver

import (
	"context"
	"errors"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func AutocompleteSelections(ctx context.Context, req Request, getModels func(context.Context, ...string) ([]modelrepo.Provider, error), policy Policy) ([]Selection, error) {
	if len(req.ModelNames) != 1 || len(req.ProviderTypes) != 1 || req.ContextLength < 0 {
		return nil, errors.New("autocomplete requires one explicit provider and model and a non-negative context length")
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelrepo.Provider.CanAutocomplete)
	if err != nil {
		return nil, err
	}
	return orderSelections(candidates, policy)
}
