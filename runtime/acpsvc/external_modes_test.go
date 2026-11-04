package acpsvc

import (
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

func claudeModeOptions() []libacp.SessionConfigOption {
	values := []libacp.SessionConfigValue{{Value: "default"}, {Value: "acceptEdits"}, {Value: "plan"}, {Value: "auto"}, {Value: "bypassPermissions"}}
	return []libacp.SessionConfigOption{
		{ID: "mode", Name: "Mode", Category: configCategoryMode, Type: "select", CurrentValue: "default", Options: libacp.NewSessionConfigValues(values)},
		{ID: "model", Name: "Model", Category: configCategoryModel, Type: "select", CurrentValue: "opus", Options: libacp.NewSessionConfigValues([]libacp.SessionConfigValue{{Value: "opus"}})},
	}
}

func TestUnit_RestrictModes_KeepsOnlyAllowedModes(t *testing.T) {
	got := restrictModes(claudeModeOptions(), []string{"default", "plan"})
	require.Len(t, got, 2)
	var modes []string
	for _, v := range got[0].Options.AllValues() {
		modes = append(modes, v.Value)
	}
	require.Equal(t, []string{"default", "plan"}, modes)
	require.Len(t, got[1].Options.AllValues(), 1, "non-mode options are untouched")
}

func TestUnit_RestrictModes_EmptyAllowlistKeepsAll(t *testing.T) {
	got := restrictModes(claudeModeOptions(), nil)
	require.Len(t, got[0].Options.AllValues(), 5)
}

func TestUnit_BuildConfigOptions_SkipsSyntheticModeWhenAgentSendsOne(t *testing.T) {
	b := &externalBridge{
		configOptions: claudeModeOptions(),
		modeState:     &libacp.SessionModeState{CurrentModeID: "default", AvailableModes: []libacp.SessionMode{{ID: "default", Name: "Manual"}}},
	}
	got := b.buildConfigOptionsLocked()
	for _, o := range got {
		require.NotEqual(t, AgentModeConfigOptionID, o.ID, "the agent's own mode option already covers it")
	}
	require.Len(t, got, 2)
}
