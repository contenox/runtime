package agentinstance

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

func permissionRequest(sessionID libacp.SessionID) libacp.RequestPermissionRequest {
	return libacp.RequestPermissionRequest{
		SessionID: sessionID,
		ToolCall:  libacp.PermissionToolCall{ToolCallID: "call-1", Title: "do the thing"},
		Options: []libacp.PermissionOption{
			{OptionID: "yes", Name: "Allow once", Kind: libacp.PermissionAllowOnce},
			{OptionID: "no", Name: "Reject once", Kind: libacp.PermissionRejectOnce},
		},
	}
}

func hubWithFallback(fn func(context.Context, libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error)) (*viewerHub, *[]libacp.SessionID, *sync.Mutex) {
	hub := newViewerHub("inst-1", defaultJournalSize)
	var mu sync.Mutex
	var denies []libacp.SessionID
	hub.onUnsupervisedDeny = func(sessionID libacp.SessionID) {
		mu.Lock()
		denies = append(denies, sessionID)
		mu.Unlock()
	}
	hub.onUnsupervisedRequest = fn
	return hub, &denies, &mu
}

func TestUnit_PermissionFallback_UnwiredStillDenies(t *testing.T) {
	hub, denies, mu := hubWithFallback(nil)

	resp, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err)
	require.Equal(t, libacp.PermissionOutcomeCancelled, resp.Outcome.Outcome)
	require.Empty(t, resp.Outcome.OptionID)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []libacp.SessionID{"sess-1"}, *denies)
}

func TestUnit_PermissionFallback_WiredAnswerIsReturned(t *testing.T) {
	var seen libacp.RequestPermissionRequest
	hub, denies, mu := hubWithFallback(func(_ context.Context, req libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		seen = req
		return libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: "yes"},
		}, nil
	})

	resp, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err)
	require.Equal(t, libacp.PermissionOutcomeSelected, resp.Outcome.Outcome)
	require.Equal(t, "yes", resp.Outcome.OptionID)
	require.Equal(t, "call-1", seen.ToolCall.ToolCallID, "the fallback receives the whole request")

	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, *denies, "a PERMITTED request must not be recorded as an unsupervised deny")
}

func TestUnit_PermissionFallback_WiredRefusalIsAudited(t *testing.T) {
	hub, denies, mu := hubWithFallback(func(_ context.Context, _ libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		return libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: "no"},
		}, nil
	})

	resp, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err)
	require.Equal(t, "no", resp.Outcome.OptionID)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []libacp.SessionID{"sess-1"}, *denies)
}

func TestUnit_PermissionFallback_UnknownOptionCountsAsRefusal(t *testing.T) {
	hub, denies, mu := hubWithFallback(func(_ context.Context, _ libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		return libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: "invented"},
		}, nil
	})

	_, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *denies, 1)
}

func TestUnit_PermissionFallback_ErrorFallsBackToDeny(t *testing.T) {
	hub, denies, mu := hubWithFallback(func(_ context.Context, _ libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		return libacp.RequestPermissionResponse{}, fmt.Errorf("answerer is unavailable")
	})

	resp, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err, "an answerer's failure must not fault the downstream turn")
	require.Equal(t, libacp.PermissionOutcomeCancelled, resp.Outcome.Outcome)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []libacp.SessionID{"sess-1"}, *denies)
}

func TestUnit_PermissionFallback_ControllerWins(t *testing.T) {
	called := false
	hub, _, _ := hubWithFallback(func(_ context.Context, _ libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		called = true
		return libacp.RequestPermissionResponse{}, nil
	})

	viewer := newMockViewer("A")
	viewer.permKind = libacp.PermissionOutcomeSelected
	viewer.optionID = "yes"
	_, err := hub.attach(context.Background(), "sess-1", viewer)
	require.NoError(t, err)

	resp, err := hub.requestPermission(context.Background(), permissionRequest("sess-1"))
	require.NoError(t, err)
	require.Equal(t, "yes", resp.Outcome.OptionID)
	require.False(t, called, "an attached controller answers; the fallback is for UNATTENDED sessions only")
}

func TestUnit_PermissionFallback_CarriesInstanceIdentity(t *testing.T) {
	ctx, _, svc := setupRegistry(t)
	stub := buildStubAgent(t)
	agent := registerExternalEnv(t, ctx, svc, "ext-agent", stub, map[string]string{
		"ACP_STUB_GATED_TOOLS_NAME": "local_fs",
		"ACP_STUB_GATED_TOOL_NAME":  "write_file",
		"ACP_STUB_GATED_ARGS_JSON":  `{"path":"/tmp/x"}`,
	})

	var got UnattendedPermission
	mgr := New(svc, WithPermissionFallback(func(_ context.Context, req UnattendedPermission) (libacp.RequestPermissionResponse, error) {
		got = req
		return libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: "allow-once"},
		}, nil
	}))
	t.Cleanup(func() { _ = mgr.Close() })

	id, err := mgr.Start(ctx, "ext-agent", t.TempDir())
	require.NoError(t, err)
	sid := openSession(t, mgr, id)

	reason := promptText(t, mgr, id, sid, "gated_action")
	require.Equal(t, libacp.StopReasonEndTurn, reason,
		"a granted permission must let the turn finish instead of refusing it")

	require.Equal(t, id, got.InstanceID)
	require.Equal(t, agent.ID, got.AgentID)
	require.Equal(t, "ext-agent", got.AgentName)
	require.Equal(t, sid, got.SessionID)
	require.Equal(t, sid, got.Request.SessionID)
}
