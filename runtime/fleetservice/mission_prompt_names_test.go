package fleetservice

import (
	"context"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_MissionPrompts_NameToolsAsTheModelSeesThem(t *testing.T) {
	tools, err := missiontools.New(schemaOnlyStore{}, nil).GetToolsForToolsByName(
		missiontools.WithMissionID(context.Background(), "m-1"),
		missiontools.ToolsProviderName,
	)
	require.NoError(t, err)
	require.NotEmpty(t, tools, "the mission tools must be listed for a session on a mission")

	offered := make(map[string]bool, len(tools))
	for _, tool := range tools {
		offered[missiontools.ToolsProviderName+"."+tool.Function.Name] = true
	}

	for _, name := range []string{toolAskAttention, toolReport, toolFinish} {
		require.True(t, offered[name],
			"the prompts tell a unit to call %q, which is not among the tools the model is offered: %v", name, keys(offered))
	}

	for _, prompt := range []struct {
		what string
		text string
	}{{"preamble", missionPreamble}, {"nudge", missionNudge}} {
		for _, name := range []string{toolAskAttention, toolReport, toolFinish} {
			require.Contains(t, prompt.text, name, "the %s must name %q the way the model sees it", prompt.what, name)
		}
		for _, bare := range []string{
			missiontools.ToolNameAskAttention,
			missiontools.ToolNameReport,
			missiontools.ToolNameFinish,
		} {
			require.False(t, mentionsBare(prompt.text, bare),
				"the %s names the bare tool %q; the model's list only has %q.%s",
				prompt.what, bare, missiontools.ToolsProviderName, bare)
		}
	}
}

func mentionsBare(text, tool string) bool {
	prefix := missiontools.ToolsProviderName + "."
	for i := 0; ; {
		idx := strings.Index(text[i:], tool)
		if idx < 0 {
			return false
		}
		at := i + idx
		if at < len(prefix) || text[at-len(prefix):at] != prefix {
			return true
		}
		i = at + len(tool)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type schemaOnlyStore struct{}

func (schemaOnlyStore) AddReport(context.Context, string, *missionservice.Report) error {
	return nil
}

func (schemaOnlyStore) Heartbeat(context.Context, string, string) (*missionservice.Mission, error) {
	return nil, nil
}

func (schemaOnlyStore) SetPlan(context.Context, string, []missionservice.PlanEntry, string) (*missionservice.Mission, error) {
	return nil, nil
}

func (schemaOnlyStore) Finish(context.Context, string, missionservice.Status, string) (*missionservice.Mission, error) {
	return nil, nil
}

var _ taskengine.Tool
