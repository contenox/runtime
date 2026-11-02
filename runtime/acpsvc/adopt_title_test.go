package acpsvc

import (
	"context"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/stretchr/testify/require"
)

func TestLoopback_Adopt_TitlesTheTakenOverSessionWithTheMissionIntent(t *testing.T) {
	f := newInstancesFixture(t)
	agentName := registerStubAgentInDB(t, f.db, "claude-stub-adopt-title", nil)
	ctx := context.Background()
	cwd := t.TempDir()
	instanceID, downstreamID := dispatchLike(t, f.mgr, agentName, cwd)

	missions := missionservice.New(f.db)
	m := &missionservice.Mission{Intent: "Review the uncommitted changes", AgentName: agentName, HITLPolicyName: "read_only", Cwd: cwd}
	require.NoError(t, missions.Create(ctx, m))
	_, err := missions.Bind(ctx, m.ID, string(downstreamID), instanceID)
	require.NoError(t, err)

	c := f.connect()
	_, err = c.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := c.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       adoptMetaJSON(instanceID, downstreamID),
	})
	require.NoError(t, err)

	list, err := c.client.ListSessions(ctx, libacp.ListSessionsRequest{})
	require.NoError(t, err)
	var title string
	for _, s := range list.Sessions {
		if s.SessionID == newResp.SessionID {
			title = s.Title
		}
	}
	require.Equal(t, "Review the uncommitted changes", title, "a taken-over mission keeps its intent as its name after a reload")
}
