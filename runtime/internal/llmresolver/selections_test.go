package llmresolver_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_ChatSelections_KeepsSameModelOnDistinctBackends(t *testing.T) {
	providers := []modelrepo.Provider{
		&modelrepo.MockProvider{
			ID:          "vertex-google:gemini-test",
			Name:        "gemini-test",
			CanChatFlag: true,
			Backends:    []string{"https://us-central1.example/v1"},
		},
		&modelrepo.MockProvider{
			ID:          "vertex-google:gemini-test",
			Name:        "gemini-test",
			CanChatFlag: true,
			Backends:    []string{"https://global.example/v1"},
		},
	}
	getModels := func(context.Context, ...string) ([]modelrepo.Provider, error) {
		return providers, nil
	}

	selections, err := llmresolver.ChatSelections(
		context.Background(),
		llmresolver.Request{ModelNames: []string{"gemini-test"}},
		getModels,
		llmresolver.Randomly,
	)
	require.NoError(t, err)
	require.Len(t, selections, 2, "each backend must stay an independent failover candidate")

	backends := map[string]bool{}
	for _, sel := range selections {
		backends[sel.Backend] = true
	}
	require.True(t, backends["https://us-central1.example/v1"])
	require.True(t, backends["https://global.example/v1"])
}

func TestUnit_StreamSelections_PolicyPickFirstThenAlternates(t *testing.T) {
	pinned := &modelrepo.MockProvider{
		ID:            "vertex-google:gemini-test",
		Name:          "gemini-test",
		CanStreamFlag: true,
		Backends:      []string{"https://global.example/v1"},
	}
	other := &modelrepo.MockProvider{
		ID:            "vertex-google:gemini-test",
		Name:          "gemini-test",
		CanStreamFlag: true,
		Backends:      []string{"https://us-central1.example/v1"},
	}
	getModels := func(context.Context, ...string) ([]modelrepo.Provider, error) {
		return []modelrepo.Provider{other, pinned}, nil
	}
	pickPinned := func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error) {
		return pinned, pinned.Backends[0], nil
	}

	selections, err := llmresolver.StreamSelections(
		context.Background(),
		llmresolver.Request{ModelNames: []string{"gemini-test"}},
		getModels,
		pickPinned,
	)
	require.NoError(t, err)
	require.Len(t, selections, 2)
	require.Equal(t, "https://global.example/v1", selections[0].Backend, "the policy's pick leads")
	require.Equal(t, "https://us-central1.example/v1", selections[1].Backend, "the remaining backend follows as the failover alternate")
}
