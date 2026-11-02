package chainagents_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/chainagents"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func chainWithTools(tools ...[]string) *taskengine.TaskChainDefinition {
	chain := &taskengine.TaskChainDefinition{ID: "c"}
	for _, ts := range tools {
		chain.Tasks = append(chain.Tasks, taskengine.TaskDefinition{
			ID:            "t",
			Handler:       taskengine.HandleChatCompletion,
			ExecuteConfig: &taskengine.LLMExecutionConfig{Tools: ts},
		})
	}
	return chain
}

func TestUnit_ChainGrantsMissionTools(t *testing.T) {
	require.True(t, chainagents.ChainGrantsMissionTools(chainWithTools([]string{"mission"})))
	require.True(t, chainagents.ChainGrantsMissionTools(chainWithTools([]string{"local_fs"}, []string{"mission", "native-git"})))
	require.True(t, chainagents.ChainGrantsMissionTools(chainWithTools([]string{"*"})))
	require.False(t, chainagents.ChainGrantsMissionTools(chainWithTools([]string{"*", "!mission"})))
	require.False(t, chainagents.ChainGrantsMissionTools(chainWithTools([]string{"local_fs"})))
	require.False(t, chainagents.ChainGrantsMissionTools(chainWithTools()))
	require.False(t, chainagents.ChainGrantsMissionTools(nil))
}

func TestUnit_MissionCapableReadsTheAgentsChainFile(t *testing.T) {
	dir := t.TempDir()
	writeChain(t, dir, "agent-noop.json", "agent-noop")

	agent := &runtimetypes.Agent{Name: "agent-noop"}
	require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{Path: dir + "/agent-noop.json"}))
	require.False(t, chainagents.MissionCapable(agent))

	missing := &runtimetypes.Agent{Name: "gone"}
	require.NoError(t, missing.SetChainConfig(runtimetypes.ChainConfig{Path: dir + "/gone.json"}))
	require.False(t, chainagents.MissionCapable(missing))

	external := &runtimetypes.Agent{Name: "ext"}
	require.NoError(t, external.SetExternalACPConfig(runtimetypes.ExternalACPConfig{Transport: runtimetypes.ExternalACPTransportStdio, Command: "x"}))
	require.False(t, chainagents.MissionCapable(external))
	require.False(t, chainagents.MissionCapable(nil))
}

func TestUnit_MissionCapable_ShippedChatChainsAreNotMissionAgents(t *testing.T) {
	dir := t.TempDir()
	write := func(file, id string) *runtimetypes.Agent {
		body := `{"id":"` + id + `","tasks":[{"id":"t","handler":"chat_completion","execute_config":{"tools":["*","mission"]}}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644))
		agent := &runtimetypes.Agent{Name: id, Kind: runtimetypes.AgentKindChain}
		require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{Path: filepath.Join(dir, file)}))
		return agent
	}

	require.False(t, chainagents.MissionCapable(write("chain-agent-acp.json", "chain-acp")), "the chat chain carries mission tools to fire missions, not to run as one")
	require.False(t, chainagents.MissionCapable(write("chain-agent-acpx.json", "chain-acpx")))
	require.True(t, chainagents.MissionCapable(write("agent-reviewer.json", "agent-reviewer")))
}
