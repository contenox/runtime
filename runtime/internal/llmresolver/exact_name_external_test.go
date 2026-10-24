package llmresolver_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_AnExactModelNameNeverResolvesToItsPrefixTwin(t *testing.T) {
	providers := []modelrepo.Provider{
		&modelrepo.MockProvider{ID: "meta", Name: "meta-llama/Llama-3.1-8B-Instruct", ContextLength: 8192, CanChatFlag: true, Backends: []string{"b-meta"}},
		&modelrepo.MockProvider{ID: "nvidia", Name: "nvidia/Llama-3.1-8B-Instruct", ContextLength: 8192, CanChatFlag: true, Backends: []string{"b-nvidia"}},
	}
	getModels := func(context.Context, ...string) ([]modelrepo.Provider, error) { return providers, nil }
	for _, want := range []string{"meta", "nvidia"} {
		request := llmresolver.Request{ModelNames: []string{providers[0].ModelName()}, ContextLength: 4096}
		if want == "nvidia" {
			request.ModelNames = []string{providers[1].ModelName()}
		}
		for session := 0; session < 20; session++ {
			selections, err := llmresolver.ChatSelections(context.Background(), request, getModels, llmresolver.StickyOrRandom(fmt.Sprintf("session-%d", session)))
			require.NoError(t, err)
			require.Len(t, selections, 1, "only the provider carrying the exact name is a candidate")
			require.Equal(t, want, selections[0].Provider.GetID())
		}
	}
}

func TestUnit_ANameWithoutAnExactMatchStillResolvesByNormalisation(t *testing.T) {
	providers := []modelrepo.Provider{
		&modelrepo.MockProvider{ID: "tagged", Name: "llama2:7b", ContextLength: 8192, CanChatFlag: true, Backends: []string{"b1"}},
	}
	getModels := func(context.Context, ...string) ([]modelrepo.Provider, error) { return providers, nil }
	selections, err := llmresolver.ChatSelections(context.Background(), llmresolver.Request{ModelNames: []string{"Llama-2:7b"}, ContextLength: 4096}, getModels, llmresolver.Randomly)
	require.NoError(t, err)
	require.Len(t, selections, 1)
	require.Equal(t, "tagged", selections[0].Provider.GetID())
}
