package modelrepo_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_ThinkDeclarationRefusal_IsRegisteredPerBackendTypeAndRefusesMisuse(t *testing.T) {
	_, refused := modelrepo.ThinkDeclarationRefusal("think-refusal-unregistered")
	require.False(t, refused, "an adapter that registers nothing acts on the declaration")

	modelrepo.RegisterThinkDeclarationRefusal("Think-Refusal-Test", "the adapter sends the same request")
	reason, refused := modelrepo.ThinkDeclarationRefusal("  think-refusal-test ")
	require.True(t, refused)
	require.Equal(t, "the adapter sends the same request", reason)

	require.Panics(t, func() { modelrepo.RegisterThinkDeclarationRefusal("think-refusal-test", "again") }, "one reason per backend type")
	require.Panics(t, func() { modelrepo.RegisterThinkDeclarationRefusal("", "a reason") }, "a backend type is required")
	require.Panics(t, func() { modelrepo.RegisterThinkDeclarationRefusal("think-refusal-empty", "  ") }, "a reason is required")
}
