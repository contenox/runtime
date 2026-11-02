package missionapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/stretchr/testify/require"
)

func TestIntegration_MissionAPI_ExposesCwd(t *testing.T) {
	ctx, svc, srv := setupMissionAPI(t)
	m := &missionservice.Mission{Intent: "review", AgentName: "agent-reviewer", HITLPolicyName: testPolicy, Cwd: "/work/repo"}
	require.NoError(t, svc.Create(ctx, m))
	bare := &missionservice.Mission{Intent: "other", AgentName: "agent-reviewer", HITLPolicyName: testPolicy}
	require.NoError(t, svc.Create(ctx, bare))

	resp, err := http.Get(srv.URL + "/missions/" + m.ID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var one map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&one))
	require.Equal(t, "/work/repo", one["cwd"])

	list, err := http.Get(srv.URL + "/missions")
	require.NoError(t, err)
	defer list.Body.Close()
	var items []map[string]any
	require.NoError(t, json.NewDecoder(list.Body).Decode(&items))
	got := map[string]any{}
	for _, it := range items {
		got[it["id"].(string)] = it["cwd"]
	}
	require.Equal(t, "/work/repo", got[m.ID])
	v, present := got[bare.ID]
	require.True(t, present)
	require.Equal(t, "", v)
}
