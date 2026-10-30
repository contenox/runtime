package acpsvc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_EnvelopesList_ReportsTheShippedCatalog(t *testing.T) {
	tr := &Transport{}

	raw, rpcErr := tr.handleExtRequest(context.Background(), ExtMethodEnvelopesList, nil)
	require.Nil(t, rpcErr)

	var got struct {
		Envelopes []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"envelopes"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))

	names := make([]string, 0, len(got.Envelopes))
	for _, env := range got.Envelopes {
		names = append(names, env.Name)
		assert.NotEmpty(t, env.Description, "%s carries its description", env.Name)
	}
	assert.ElementsMatch(t, []string{"read_only", "ask_always", "auto_edit", "default", "strict", "acpx", "oracle"}, names)
}

func TestUnit_AgentProblems_ReturnsTheLastSyncProblems(t *testing.T) {
	tr := &Transport{}
	raw, rpcErr := tr.handleExtRequest(context.Background(), ExtMethodAgentProblems, nil)
	require.Nil(t, rpcErr)

	var got struct {
		Problems []struct {
			Source string `json:"source"`
			Action string `json:"action"`
		} `json:"problems"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	_ = got
}

func TestUnit_DeclaredAgentPolicy_UsesTheAgentsOwnRenderedPolicy(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	tr := &Transport{deps: Deps{ContenoxDir: home}}
	got := tr.declaredAgentPolicy(context.Background(), "reviewer")
	assert.Equal(t, "hitl-policy-reviewer.json", got, "a declared agent's mission runs under its own posture")
	assert.Empty(t, tr.declaredAgentPolicy(context.Background(), "no-such-agent"))
}

func TestUnit_ChainRegistry_ReloadPicksUpARewrittenChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.json")
	write := func(id string) {
		body := `{"id":"` + id + `","tasks":[{"id":"t","handler":"chat_completion","execute_config":{"model":"m","provider":"p"}}]}`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("first")
	registry, err := LoadChainRegistryPath(path)
	require.NoError(t, err)
	require.Equal(t, "first", registry.Default().ID)

	write("second")
	require.NoError(t, registry.Reload())
	assert.Equal(t, "second", registry.Default().ID, "a new turn sees the recompiled chain")
}
