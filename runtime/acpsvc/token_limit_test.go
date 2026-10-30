package acpsvc

import (
	"context"
	"encoding/json"
	libacp "github.com/contenox/runtime/libacp"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/statetype"
	"github.com/stretchr/testify/require"
)

func tokenValues(cap, cur int) []string {
	var out []string
	for _, v := range tokenLimitConfigValues(cap, cur).AllValues() {
		out = append(out, v.Value)
	}
	return out
}

func TestUnit_TokenLimitValues(t *testing.T) {
	// Unknown max: chain default first, full ladder.
	require.Equal(t, []string{"0", "65536", "131072", "262144", "524288", "1048576"}, tokenValues(0, 0))
	// Known max 400000: model maximum first, ladder capped below it.
	require.Equal(t, []string{"400000", "0", "65536", "131072", "262144"}, tokenValues(400000, 0))
	require.Equal(t, "Model maximum (400k tokens)", tokenLimitConfigValues(400000, 0).AllValues()[0].Name)
	// Smaller model: options follow.
	require.Equal(t, []string{"100000", "0", "65536"}, tokenValues(100000, 0))
	require.Equal(t, "Chain default", tokenLimitConfigValues(0, 0).AllValues()[0].Name)
}

func TestUnit_ContextCapFollowsModel(t *testing.T) {
	states := []statetype.BackendRuntimeState{{
		Backend: runtimetypes.Backend{Type: "openai-codex"},
		PulledModels: []statetype.ModelPullStatus{
			{Model: "big", ContextLength: 400000, CanChat: true},
			{Model: "small", ContextLength: 100000, CanChat: true},
		},
	}}
	require.Equal(t, 400000, contextCapFor(states, "openai-codex", "big"))
	require.Equal(t, 100000, contextCapFor(states, "openai-codex", "small"))
	require.Equal(t, 0, contextCapFor(states, "openai-codex", "unknown"))
	require.Equal(t, 0, contextCapFor(states, "", ""))
}

func TestUnit_InitializeExposesAvailableCommands(t *testing.T) {
	tr := &Transport{}
	resp, err := tr.Initialize(context.Background(), libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	var meta map[string][]libacp.AvailableCommand
	require.NoError(t, json.Unmarshal(resp.Meta, &meta))
	cmds := meta[AvailableCommandsMetaKey]
	require.Equal(t, tr.acpCommands(), cmds)
	names := map[string]bool{}
	for _, c := range cmds {
		names[c.Name] = true
	}
	require.True(t, names["model"])
	require.True(t, names["help"])
}

func TestUnit_ProvisionalTitle(t *testing.T) {
	require.Equal(t, "hello world", provisionalTitle("\n  \n hello world \nsecond"))
	require.Equal(t, "/model", provisionalTitle("/model gpt-5"))
	require.Equal(t, "", provisionalTitle("  \n "))
	long := provisionalTitle("please refactor the authentication middleware so that it supports rotating signing keys and caching")
	require.LessOrEqual(t, len([]rune(long)), 60)
	require.True(t, strings.HasSuffix(long, "…"))
	require.NotContains(t, long, "caching")
}

func TestUnit_ProvisionalTitleLifecycle(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{deps: Deps{DB: db}}
	sess := &sessionEntry{InternalSessionID: "sess-x"}

	tr.setProvisionalTitle(ctx, "acp-1", sess, "first question\nmore")
	require.Equal(t, "first question", tr.sessionInfoTitle(ctx, "sess-x"))

	// A later prompt (e.g. after a failed first turn) must not overwrite it.
	tr.setProvisionalTitle(ctx, "acp-1", sess, "different")
	require.Equal(t, "first question", tr.sessionInfoTitle(ctx, "sess-x"))

	// Other sessions are untouched; empty input sets nothing.
	tr.setProvisionalTitle(ctx, "acp-2", &sessionEntry{InternalSessionID: "sess-y"}, "  ")
	require.Equal(t, "", tr.sessionInfoTitle(ctx, "sess-y"))
}

func TestUnit_ModelOptionEmptyWhenNoDefault(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{deps: Deps{DB: db}}
	sess := &sessionEntry{driver: &nativeDriver{t: tr}}
	opt := tr.modelConfigOption(ctx, sess)
	require.Equal(t, "", opt.CurrentValue)
	require.NotContains(t, opt.CurrentValue, "qwen")
	require.Error(t, requireDefaultModel(tr.chainTemplateVars(sess)))
	require.NoError(t, requireDefaultModel(map[string]string{"model": "x"}))
}
