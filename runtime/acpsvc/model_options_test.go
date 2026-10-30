package acpsvc

import (
	"encoding/json"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/statetype"
	"github.com/stretchr/testify/require"
)

func conn(id, name, typ, errMsg string, models ...statetype.ModelPullStatus) statetype.BackendRuntimeState {
	return statetype.BackendRuntimeState{
		Backend:      runtimetypes.Backend{ID: id, Name: name, Type: typ},
		Error:        errMsg,
		PulledModels: models,
	}
}

func chat(name string, ctx int) statetype.ModelPullStatus {
	return statetype.ModelPullStatus{Model: name, CanChat: true, ContextLength: ctx}
}

func TestUnit_ModelOptionsGroupByConnection(t *testing.T) {
	states := []statetype.BackendRuntimeState{
		conn("d1", "deepseek-a", "deepseek", "", chat("deepseek-chat", 64000)),
		conn("d2", "deepseek-b", "deepseek", "", chat("deepseek-chat", 128000), chat("deepseek-reasoner", 128000)),
		conn("dead", "down", "openai", "unreachable", chat("gpt-x", 1000)),
	}
	opts := buildModelOptions(states)
	require.Len(t, opts.Groups, 3, "any + two reachable connections; the failing one is skipped")
	require.Equal(t, "any", opts.Groups[0].Group)
	require.Equal(t, "Any provider", opts.Groups[0].Name)
	require.Equal(t, []string{"any/deepseek-chat", "any/deepseek-reasoner"}, groupValues(opts.Groups[0]))
	require.Equal(t, "d1", opts.Groups[1].Group)
	require.Equal(t, []string{"connection:d1/deepseek-chat"}, groupValues(opts.Groups[1]))
	require.Equal(t, []string{"connection:d2/deepseek-chat", "connection:d2/deepseek-reasoner"}, groupValues(opts.Groups[2]))
	var meta map[string]any
	require.NoError(t, json.Unmarshal(opts.Groups[1].Meta, &meta))
	require.Equal(t, "deepseek", meta["contenox.providerType"])
	for _, v := range opts.AllValues() {
		require.NotContains(t, v.Value, "gpt-x", "unserved/unreachable models are never listed")
	}
}

func groupValues(g libacp.SessionConfigGroup) []string {
	var out []string
	for _, v := range g.Options {
		out = append(out, v.Value)
	}
	return out
}

func TestUnit_ModelSelectionMapping(t *testing.T) {
	states := []statetype.BackendRuntimeState{
		conn("c1", "chatgpt", "openai-codex", "", chat("gpt-5.6-luna", 272000), chat("org/model", 1000)),
		conn("d1", "ds1", "deepseek", "", chat("deepseek-chat", 64000)),
		conn("d2", "ds2", "deepseek", "", chat("deepseek-chat", 128000)),
	}
	v, ok := canonicalModelSelection(states, "openai-codex", "gpt-5.6-luna")
	require.True(t, ok)
	require.Equal(t, "connection:c1/gpt-5.6-luna", v, "type served by one connection pins it")
	v, _ = canonicalModelSelection(states, "deepseek", "deepseek-chat")
	require.Equal(t, "any/deepseek-chat", v, "type served by two connections is any")
	_, ok = canonicalModelSelection(states, "openai-codex", "missing")
	require.False(t, ok, "unserved default is not set")

	// Model names containing "/" split safely; bare values are rejected.
	p, m, ok := resolveModelValue(states, "connection:c1/org/model")
	require.True(t, ok)
	require.Equal(t, "connection:c1", p)
	require.Equal(t, "org/model", m)
	_, _, ok = resolveModelValue(states, "gpt-5.6-luna")
	require.False(t, ok)

	// Legacy type/model values map onto connections or any.
	p, m, ok = resolveModelValue(states, "openai-codex/gpt-5.6-luna")
	require.True(t, ok)
	require.Equal(t, "connection:c1", p)
	require.Equal(t, "gpt-5.6-luna", m)
	p, _, ok = resolveModelValue(states, "deepseek/deepseek-chat")
	require.True(t, ok)
	require.Equal(t, "any", p)
}

func TestUnit_ContextCapMinAcrossServingConnections(t *testing.T) {
	states := []statetype.BackendRuntimeState{
		conn("d1", "ds1", "deepseek", "", chat("deepseek-chat", 64000)),
		conn("d2", "ds2", "deepseek", "", chat("deepseek-chat", 128000)),
	}
	require.Equal(t, 64000, contextCapFor(states, "any", "deepseek-chat"))
	require.Equal(t, 128000, contextCapFor(states, "connection:d2", "deepseek-chat"))
}

func TestUnit_ModelOptionCurrentEmptyWhenDefaultUnserved(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{
		deps:            Deps{DB: db},
		defaultProvider: "openai-codex",
		defaultModel:    "gone",
		statesFn: func() []statetype.BackendRuntimeState {
			return []statetype.BackendRuntimeState{conn("c1", "chatgpt", "openai-codex", "", chat("other", 1))}
		},
	}
	sess := &sessionEntry{driver: &nativeDriver{t: tr}}
	require.Equal(t, "", tr.modelConfigOption(ctx, sess).CurrentValue)
}

func TestUnit_WorkspaceConfigOptionsExtMethod(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{deps: Deps{DB: db}}
	res, rpcErr := tr.handleExtRequest(ctx, ExtMethodWorkspaceConfigOptions, nil)
	require.Nil(t, rpcErr)
	var payload workspaceConfigOptionsPayload
	require.NoError(t, json.Unmarshal(res, &payload))
	require.NotNil(t, payload.ConfigOptions)
}
