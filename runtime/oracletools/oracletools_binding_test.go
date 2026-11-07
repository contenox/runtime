package oracletools_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/oracletools"
	"github.com/stretchr/testify/require"
)

func TestUnit_OracleTools_BindingResolverOverridesTheProviders(t *testing.T) {
	provider := &recordingAnswerer{}
	own := &recordingAnswerer{}
	p := oracletools.New(provider)
	binding := oracletools.NewAskBinding(askID, oracletools.AskKindPermission, inputJSON).WithResolver(own)
	ctx := oracletools.WithBinding(context.Background(), binding)

	out, err := submit(t, p, ctx, map[string]any{"verdict": "approve", "askId": askID})
	require.NoError(t, err)
	require.True(t, out.accepted)
	require.Equal(t, "approved", out.outcome)
	require.Equal(t, []string{askID}, own.calls)
	require.Equal(t, []bool{true}, own.decisions)
	require.Empty(t, provider.calls, "a bound ask that carries its own resolver never reaches the provider's")
	require.Equal(t, oracletools.OutcomeApproved, binding.Outcome())
}

func TestUnit_OracleTools_BindingWithoutResolverUsesTheProviders(t *testing.T) {
	provider := &recordingAnswerer{}
	p := oracletools.New(provider)
	binding := oracletools.NewAskBinding(askID, oracletools.AskKindPermission, inputJSON)
	ctx := oracletools.WithBinding(context.Background(), binding)

	out, err := submit(t, p, ctx, map[string]any{"verdict": "deny", "guidance": "ask first", "askId": askID})
	require.NoError(t, err)
	require.True(t, out.accepted)
	require.Equal(t, "denied", out.outcome)
	require.Equal(t, []string{askID}, provider.calls)
	require.Equal(t, []bool{false}, provider.decisions)
	require.Equal(t, []string{"ask first"}, provider.guidances)
}
