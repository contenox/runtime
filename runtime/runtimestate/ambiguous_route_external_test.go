package runtimestate_test

import (
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_ATypeSeveralConnectionsShareIsRefusedAsARouteNamingThem(t *testing.T) {
	f := newCredentialFixture(t)
	first := newRecordingProvider(t, "shared-chat")
	second := newRecordingProvider(t, "shared-chat")
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-one", Name: "one", Type: "vllm", BaseURL: first.URL}, &runtimestate.ProviderConfig{Type: "vllm", CredentialSource: runtimestate.CredentialNone})
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-two", Name: "two", Type: "vllm", BaseURL: second.URL}, &runtimestate.ProviderConfig{Type: "vllm", CredentialSource: runtimestate.CredentialNone})
	state := f.cycle(t)
	providers := state.ExecutionProviders(libtracker.NoopTracker{})

	_, err := providers(f.ctx, "vllm")
	var ambiguous *runtimestate.AmbiguousRouteError
	require.ErrorAs(t, err, &ambiguous, "a type two connections share is no route: %v", err)
	require.Equal(t, []string{"one", "two"}, ambiguous.Connections)
	require.Contains(t, err.Error(), "/role <role> <connection> <model>")

	selected, err := providers(f.ctx, runtimestate.ConnectionSelector("conn-two"))
	require.NoError(t, err)
	require.Len(t, selected, 1, "a selector names one connection")
	require.Equal(t, []string{second.URL}, selected[0].GetBackendIDs())
}
