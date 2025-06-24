package llmrepo

import (
	"context"
	"fmt"

	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/libs/libmodelprovider"
)

type MockModelRepo struct {
	Provider libmodelprovider.Provider
}

func (m *MockModelRepo) GetProvider(ctx context.Context) (libmodelprovider.Provider, error) {
	if m.Provider == nil {
		return nil, fmt.Errorf("provider is nil for prompt execution")
	}
	return m.Provider, nil
}

// TODO: Implement GetRuntime method
func (m *MockModelRepo) GetRuntime(ctx context.Context) runtimestate.ProviderFromRuntimeState {
	return func(_ context.Context, providerTypes ...string) ([]libmodelprovider.Provider, error) {
		return []libmodelprovider.Provider{
			m.Provider,
		}, nil
	}
}
