package llmrepo

import (
	"context"
	"fmt"

	libmodelprovider "github.com/contenox/modelprovider"
	"github.com/contenox/modelprovider/llmresolver"
	"github.com/contenox/runtime-mvp/core/ollamatokenizer"
)

type MockModelRepo struct {
	Provider libmodelprovider.Provider
}

func (m *MockModelRepo) GetDefaultSystemProvider(ctx context.Context) (libmodelprovider.Provider, error) {
	if m.Provider == nil {
		return nil, fmt.Errorf("provider is nil for prompt execution")
	}
	return m.Provider, nil
}

// TODO: Implement GetRuntime method
func (m *MockModelRepo) GetRuntime(ctx context.Context) llmresolver.ProviderFromRuntimeState {
	return func(_ context.Context, providerTypes ...string) ([]libmodelprovider.Provider, error) {
		return []libmodelprovider.Provider{
			m.Provider,
		}, nil
	}
}

func (m *MockModelRepo) GetTokenizer(ctx context.Context) (ollamatokenizer.Tokenizer, error) {
	if m.Provider == nil {
		return nil, fmt.Errorf("provider is nil for prompt execution")
	}
	return ollamatokenizer.MockTokenizer{}, nil
}

func (m *MockModelRepo) GetAvailableProviders(ctx context.Context) ([]libmodelprovider.Provider, error) {
	return []libmodelprovider.Provider{
		m.Provider,
	}, nil
}
