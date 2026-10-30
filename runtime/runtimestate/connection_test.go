package runtimestate

import (
	"testing"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_AnyProviderMatchesEveryConnection(t *testing.T) {
	a := runtimetypes.Backend{ID: "a", Name: "ds-1", Type: "deepseek"}
	b := runtimetypes.Backend{ID: "b", Name: "ds-2", Type: "deepseek"}
	for _, be := range []runtimetypes.Backend{a, b} {
		require.True(t, MatchesBackend(AnyProvider, be))
		require.True(t, matchesProvider(&be, []string{AnyProvider}))
	}
	require.True(t, matchesProvider(&a, []string{ConnectionSelector("a")}))
	require.False(t, matchesProvider(&b, []string{ConnectionSelector("a")}))
	states := map[string]BackendRuntimeState{"a": {Backend: a}, "b": {Backend: b}}
	require.Error(t, refuseAmbiguousRoute(states, []string{"deepseek"}))
	require.NoError(t, refuseAmbiguousRoute(states, []string{AnyProvider}))
}

func TestUnit_LocalProviderAdapterAnyReturnsAll(t *testing.T) {
	get := LocalProviderAdapter(t.Context(), nil, map[string]BackendRuntimeState{})
	got, err := get(t.Context(), AnyProvider)
	require.NoError(t, err)
	require.Empty(t, got)
}
