package compatapi

import (
	"testing"

	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_BuildTemplateVars_SeedsTheDefaultModelARecoveryFallsBackTo(t *testing.T) {
	chain := &taskengine.TaskChainDefinition{Tasks: []taskengine.TaskDefinition{
		{ID: "agent", ExecuteConfig: &taskengine.LLMExecutionConfig{Model: "{{var:model}}", Provider: "{{var:provider}}"}},
		{ID: "recovery", ExecuteConfig: &taskengine.LLMExecutionConfig{Model: "{{var:alt_model|var:default_model}}", Provider: "{{var:alt_provider|var:default_provider}}"}},
	}}

	vars := buildTemplateVars(chain, stateservice.RuntimeDefaults{Model: "gemini-3-flash-preview", Provider: "vertex-google"}, "requested-model", nil)
	require.Equal(t, "requested-model", vars["model"])
	require.Equal(t, "gemini-3-flash-preview", vars["default_model"])
	require.Equal(t, "vertex-google", vars["default_provider"])

	vars = buildTemplateVars(chain, stateservice.RuntimeDefaults{Provider: "vertex-google"}, "requested-model", nil)
	require.Equal(t, "requested-model", vars["default_model"], "with no configured default the requested model is the fallback")
}
