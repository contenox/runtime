package runtimestate

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_AvailabilityScopeFollowsTheConnectionCredential(t *testing.T) {
	s := &State{}
	backend := &runtimetypes.Backend{ID: "b1", Type: "bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com"}
	scope := s.availabilityScope(backend, "cred-a")
	require.NotEmpty(t, scope)
	require.Equal(t, scope, s.availabilityScope(backend, "cred-a"))
	require.NotEqual(t, scope, s.availabilityScope(backend, "cred-b"))

	modelrepo.MarkModelUnavailable(scope, "us.anthropic.claude-sonnet-5-5")
	models := []modelrepo.ObservedModel{{Name: "us.anthropic.claude-sonnet-5-5"}, {Name: "amazon.nova-pro-v1:0"}}
	require.Len(t, modelrepo.WithoutUnavailableModels(scope, models), 1)
	modelrepo.ResetUnavailableModels(scope)
	require.Len(t, modelrepo.WithoutUnavailableModels(scope, models), 2)

	other := &runtimetypes.Backend{ID: "o1", Type: "openai"}
	require.Empty(t, s.availabilityScope(other, ""))
}
