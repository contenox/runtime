package llmrepo

import (
	"context"
	"fmt"

	"github.com/contenox/contenox/core/modelprovider"
)

type MockModelRepo struct {
	Provider modelprovider.Provider
}

func (m *MockModelRepo) GetProvider(ctx context.Context) (modelprovider.Provider, error) {
	if m.Provider == nil {
		return nil, fmt.Errorf("provider is nil for prompt execution")
	}
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
