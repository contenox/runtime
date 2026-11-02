package agentinstance

import (
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_ChainSpawnerPassesServeDatabaseAndDataDir(t *testing.T) {
	chainPath := filepath.Join(t.TempDir(), "agent-reviewer.json")
	agent := &runtimetypes.Agent{Name: "agent-reviewer", Kind: runtimetypes.AgentKindChain}
	require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{Path: chainPath, ChainID: "agent-reviewer"}))

	m := New(nil,
		WithSelfExecutable("/bin/contenox-runtime"),
		WithChainArgs("--db", "/data/local.db", "--data-dir", "/data", "--provider", "ollama", "--model", "qwen3"),
	).(*manager)

	spawner, err := m.chainSpawner(agent, "/work")
	require.NoError(t, err)
	ext, ok := spawner.(*agenthost.ExternalACPAgent)
	require.True(t, ok)
	require.Equal(t, "/bin/contenox-runtime", ext.Config.Command)
	require.Equal(t, []string{ChainACPSubcommand, "--db", "/data/local.db", "--data-dir", "/data", "--provider", "ollama", "--model", "qwen3"}, ext.Config.Args)
	require.Equal(t, chainPath, ext.Config.Env[ChainPathEnvVar])
	require.Equal(t, "/work", ext.Config.Cwd)
}

func TestUnit_ChainSpawnerWithoutChainArgsRunsBareACP(t *testing.T) {
	chainPath := filepath.Join(t.TempDir(), "agent-reviewer.json")
	agent := &runtimetypes.Agent{Name: "agent-reviewer", Kind: runtimetypes.AgentKindChain}
	require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{Path: chainPath}))

	m := New(nil, WithSelfExecutable("/bin/contenox-runtime")).(*manager)
	spawner, err := m.chainSpawner(agent, "/work")
	require.NoError(t, err)
	require.Equal(t, []string{ChainACPSubcommand}, spawner.(*agenthost.ExternalACPAgent).Config.Args)
}
