package llmrepo

import (
	"context"

	"github.com/js402/cate/core/modelprovider"
)

type MockModelRepo struct {
	Provider modelprovider.Provider
}

func (m *MockModelRepo) GetProvider(ctx context.Context) (modelprovider.Provider, error) {
	return m.Provider, nil
}

func (m *MockModelRepo) GetRuntime(ctx context.Context) modelprovider.RuntimeState {
	return func(_ context.Context, providerType string) ([]modelprovider.Provider, error) {
		// Match on provider type if specified
		if providerType == "" || providerType == "Ollama" {
			return []modelprovider.Provider{m.Provider}, nil
		}
		return []modelprovider.Provider{}, nil
	}
}
