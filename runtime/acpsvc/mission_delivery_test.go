package acpsvc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func reportNotification(contenoxSessionID, text string) libacp.SessionNotification {
	update := libacp.NewAgentMessageChunk(text)
	update.Meta = json.RawMessage(`{"contenox.missionReport":{"missionId":"m-1","reportId":"r-1","kind":"result"}}`)
	return libacp.SessionNotification{SessionID: libacp.SessionID(contenoxSessionID), Update: update}
}

func TestLoopback_MissionReport_RoutedThroughSessionRouterReachesTheFiringSession(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	contenoxID := h.contenoxSessionID(newResp.SessionID)
	require.NotEmpty(t, contenoxID, "the session must carry a contenox id — it is what a mission names as its parent")

	err = h.router.DeliverToContenoxSession(ctx, contenoxID, reportNotification(contenoxID, "unit chain-acp reported (result): done"))
	require.NoError(t, err, "the router must resolve the firing session to its live connection")

	got := h.lc.drain(t, 1)[0]
	require.Equal(t, newResp.SessionID, got.SessionID,
		"the report is re-addressed to the ACP session id the client knows, not the contenox id the router used")
	require.Contains(t, got.Update.Content.Text, "unit chain-acp reported")
	require.Contains(t, string(got.Update.Meta), "contenox.missionReport",
		"the attribution must survive so the client renders a report, not chat text")
}

func TestLoopback_MissionReport_UnknownSessionIsNotLive(t *testing.T) {
	h := newLoopbackHarness(t)

	err := h.router.DeliverToContenoxSession(context.Background(), "cnx-nobody-owns-this",
		reportNotification("cnx-nobody-owns-this", "orphan report"))
	require.ErrorIs(t, err, ErrSessionNotLive)
}

func TestLoopback_MissionReport_IsPersistedIntoTheTranscript(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	contenoxID := h.contenoxSessionID(newResp.SessionID)
	require.NoError(t, h.router.DeliverToContenoxSession(ctx, contenoxID,
		reportNotification(contenoxID, "unit chain-acp reported (result): the README now names the runtime")))
	h.lc.drain(t, 1)

	history := h.history(t, contenoxID)
	require.Len(t, history, 1, "the delivered report is one assistant message in the transcript")
	require.Equal(t, "assistant", history[0].Role)
	require.Contains(t, history[0].Content, "the README now names the runtime")
}

func TestLoopback_SlashCommand_TurnIsPersisted(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	resp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("/help")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, resp.StopReason)
	h.lc.drain(t, 1)

	history := h.history(t, h.contenoxSessionID(newResp.SessionID))
	require.Len(t, history, 2, "a command turn is the line typed plus the answer given")
	require.Equal(t, "user", history[0].Role)
	require.Equal(t, "/help", history[0].Content, "the transcript shows what the operator actually typed")
	require.Equal(t, "assistant", history[1].Role)
	require.Contains(t, history[1].Content, "Available commands")
}

func TestLoopback_SlashCommand_HistoryRewritingCommandsAreNotPersisted(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("/clear")},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	require.Empty(t, h.history(t, h.contenoxSessionID(newResp.SessionID)),
		"/clear leaves an empty transcript, including of itself")
}

func TestLoopback_SessionList_CarriesMissionAttribution(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	unit, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       missionservice.MarshalMissionMeta("m-42"),
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	chat, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	list, err := h.client.ListSessions(ctx, libacp.ListSessionsRequest{})
	require.NoError(t, err)

	byID := make(map[libacp.SessionID]libacp.SessionInfo, len(list.Sessions))
	for _, s := range list.Sessions {
		byID[s.SessionID] = s
	}
	unitInfo, ok := byID[unit.SessionID]
	require.True(t, ok, "the unit session must be listed")
	missionID, found := missionservice.ParseMissionMeta(unitInfo.Meta)
	require.True(t, found, "a unit session must be identifiable as one from session/list alone")
	require.Equal(t, "m-42", missionID)

	chatInfo, ok := byID[chat.SessionID]
	require.True(t, ok)
	_, found = missionservice.ParseMissionMeta(chatInfo.Meta)
	require.False(t, found, "an ordinary chat carries no mission attribution")
}

func (h *loopbackHarness) contenoxSessionID(sid libacp.SessionID) string {
	h.tr.sessionMu.Lock()
	defer h.tr.sessionMu.Unlock()
	entry, ok := h.tr.sessions[sid]
	if !ok {
		return ""
	}
	return entry.InternalSessionID
}

func (h *loopbackHarness) history(t *testing.T, contenoxSessionID string) []taskengine.Message {
	t.Helper()
	mgr := chatservice.NewManager("loopback-ws")
	msgs, err := mgr.ListMessages(context.Background(), h.tr.deps.DB.WithoutTransaction(), contenoxSessionID)
	require.NoError(t, err)
	return msgs
}

func TestLoopback_MissionTurnAndReport_SurviveAReload(t *testing.T) {
	f := newInstancesFixture(t)
	ctx := context.Background()
	cwd := t.TempDir()

	c1 := f.connect()
	_, err := c1.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := c1.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: cwd, McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	c1.lc.drain(t, 1)

	_, err = c1.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("/help")},
	})
	require.NoError(t, err)
	c1.lc.drain(t, 1)

	contenoxID := contenoxSessionIDOf(t, c1.tr, newResp.SessionID)
	require.NoError(t, c1.tr.DeliverToContenoxSession(ctx, contenoxID,
		reportNotification(contenoxID, "unit chain-acp reported (result): mission complete")))
	c1.lc.drain(t, 1)

	c1.drop()
	c2 := f.connect()
	_, err = c2.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	_, err = c2.client.LoadSession(ctx, libacp.LoadSessionRequest{SessionID: newResp.SessionID, Cwd: cwd})
	require.NoError(t, err)

	var replayed []string
	for _, note := range c2.lc.drain(t, 3) {
		if note.Update.Content != nil {
			replayed = append(replayed, note.Update.Content.Text)
		}
	}
	joined := strings.Join(replayed, "\n")
	require.Contains(t, joined, "/help", "the reloaded session shows the command the operator typed")
	require.Contains(t, joined, "Available commands", "…and the answer it gave")
	require.Contains(t, joined, "mission complete", "…and the report that landed while they were attached")
}

func contenoxSessionIDOf(t *testing.T, tr *Transport, sid libacp.SessionID) string {
	t.Helper()
	tr.sessionMu.Lock()
	defer tr.sessionMu.Unlock()
	entry := tr.sessions[sid]
	require.NotNil(t, entry, "session %q not open on this connection", sid)
	return entry.InternalSessionID
}
