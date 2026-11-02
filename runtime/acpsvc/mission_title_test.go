package acpsvc

import (
	"context"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_MissionTitleWinsOverPreambleFirstMessage(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{deps: Deps{DB: db, WorkspaceID: "ws-mission-title"}}
	store := runtimetypes.New(db.WithoutTransaction())

	require.Equal(t, "review the working tree", tr.persistMissionTitle(ctx, store, "sess-m", "  review   the working\ntree "))
	require.Equal(t, "review the working tree", tr.sessionInfoTitle(ctx, "sess-m"))
	require.Equal(t, "review the working tree", tr.sessionListTitle(ctx, chatservice.NewManager("ws-mission-title"), db.WithoutTransaction(), "sess-m", "fallback"))

	tr.setProvisionalTitle(ctx, "acp-m", &sessionEntry{InternalSessionID: "sess-m"}, "You are running as an UNATTENDED mission unit.")
	require.Equal(t, "review the working tree", tr.sessionInfoTitle(ctx, "sess-m"))

	require.Equal(t, "", tr.persistMissionTitle(ctx, store, "sess-n", "   "))
	require.Equal(t, "", readMissionTitle(ctx, db.WithoutTransaction(), "sess-n"))
}

func TestLoopback_NewSession_MissionMetaTitlesSessionWithIntent(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       missionservice.MarshalMissionMetaWithIntent("mission-1", "review the working tree"),
	})
	require.NoError(t, err)

	notes := h.lc.drain(t, 2)
	var title string
	for _, n := range notes {
		if n.Update.SessionUpdate == libacp.SessionUpdateSessionInfo {
			title = n.Update.Title
		}
	}
	require.Equal(t, "review the working tree", title)

	fake := &loopbackAgent{promptFunc: func(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
	}}
	h.swapAgent(newResp.SessionID, fake)

	h.tr.sessionMu.Lock()
	internalID := h.tr.sessions[newResp.SessionID].InternalSessionID
	h.tr.sessionMu.Unlock()

	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []libacp.ContentBlock{
			libacp.NewTextContent("You are running as an UNATTENDED mission unit."),
			libacp.NewTextContent("review the working tree"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "review the working tree", h.tr.sessionInfoTitle(ctx, internalID))
}
