package gemini_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo/gemini"
	"github.com/stretchr/testify/require"
)

func TestUnit_GeminiArgonPublishedFacts(t *testing.T) {
	facts, ok := gemini.ModelFactsFor("publishers/google/models/gemini-4-argon")
	require.True(t, ok)
	require.Equal(t, 1048576, facts.ContextLength)
	require.Equal(t, 65536, facts.MaxOutputTokens)
	require.Equal(t, []string{"LOW", "MEDIUM", "HIGH"}, facts.ThinkingLevels)
	require.False(t, facts.ThinkingBudget)
	require.False(t, facts.DisableThinking)
	declaration := gemini.ThinkingDeclarationFor("gemini-4-argon")
	require.NotNil(t, declaration)
	require.Equal(t, []string{"low", "medium", "high"}, declaration.EffortLevels)
	for _, level := range declaration.EffortLevels {
		control, err := gemini.ThinkingControlFor("gemini-4-argon", &level)
		require.NoError(t, err)
		require.Nil(t, control.Budget)
		require.True(t, control.IncludeThoughts)
	}
}
