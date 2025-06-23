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

// TODO: Implement GetRuntime method
func (m *MockModelRepo) GetRuntime(ctx context.Context) modelprovider.RuntimeState {
	return func(_ context.Context, providerTypes ...string) ([]modelprovider.Provider, error) {
		return []modelprovider.Provider{
			m.Provider,
		}, nil
	}
}
