package agentregistryapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func writeChainAgentFile(t *testing.T, tools string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-x.json")
	body := `{"id":"agent-x","tasks":[{"id":"loop","handler":"chat_completion","execute_config":{"tools":` + tools + `}}]}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestIntegration_AgentRegistryAPI_ListMarksMissionCapableAgents(t *testing.T) {
	ctx, svc, srv := setupAgentRegistryAPI(t)

	capable := &runtimetypes.Agent{Name: "agent-reviewer", Enabled: true}
	require.NoError(t, capable.SetChainConfig(runtimetypes.ChainConfig{Path: writeChainAgentFile(t, `["mission","local_fs"]`)}))
	require.NoError(t, svc.Create(ctx, capable))

	plain := &runtimetypes.Agent{Name: "agent-plain", Enabled: true}
	require.NoError(t, plain.SetChainConfig(runtimetypes.ChainConfig{Path: writeChainAgentFile(t, `["local_fs"]`)}))
	require.NoError(t, svc.Create(ctx, plain))

	require.NoError(t, svc.Create(ctx, newExternalACPAgent(t, "external")))

	resp, err := http.Get(srv.URL + "/agents")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raw []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	got := map[string]any{}
	for _, a := range raw {
		got[a["name"].(string)] = a["missionCapable"]
	}
	require.Equal(t, map[string]any{"agent-reviewer": true, "agent-plain": false, "external": false}, got)

	one, err := http.Get(srv.URL + "/agents/by-name/agent-reviewer")
	require.NoError(t, err)
	defer one.Body.Close()
	var single map[string]any
	require.NoError(t, json.NewDecoder(one.Body).Decode(&single))
	require.Equal(t, true, single["missionCapable"])
}
