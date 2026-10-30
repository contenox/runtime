package acpsvc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestLoopback_InteractiveApprovalIsRecordedInHITLApprovals(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "approvals.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())
	svc := hitlservice.NewWithDefaultPolicy(hitlservice.NewFSPolicySource(t.TempDir()), runtimetypes.LocalTenantID, store, libtracker.NoopTracker{}, "")
	h.tr.deps.DecisionRecorder = svc.(hitlservice.DecisionRecorder)

	_, err = h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.tr.sessionMu.Lock()
	internalID := h.tr.sessions[newResp.SessionID].InternalSessionID
	h.tr.sessionMu.Unlock()
	askCtx := context.WithValue(ctx, runtimetypes.SessionIDContextKey, internalID)
	rule := 1
	req := hitlservice.ApprovalRequest{
		ToolCallID:  "rec-call",
		ToolsName:   "local_shell",
		ToolName:    "exec",
		Args:        map[string]any{"command": "git push origin main"},
		PolicyName:  "hitl-policy-default.json",
		MatchedRule: &rule,
	}

	for _, option := range []string{approvalflow.OptionAllow, approvalflow.OptionDeny} {
		h.lc.setPermissionResponse(libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: option},
		})
		allowed, err := h.router.AskApproval(askCtx, req)
		require.NoError(t, err)
		require.Equal(t, option == approvalflow.OptionAllow, allowed)
	}
	h.lc.setPermissionResponse(libacp.RequestPermissionResponse{
		Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeCancelled},
	})
	_, err = h.router.AskApproval(askCtx, req)
	require.ErrorIs(t, err, context.Canceled)

	var rows []*runtimetypes.HITLApproval
	for _, st := range []runtimetypes.HITLApprovalState{runtimetypes.HITLApprovalApproved, runtimetypes.HITLApprovalDenied} {
		part, err := store.ListHITLApprovals(ctx, st, nil, 20)
		require.NoError(t, err)
		rows = append(rows, part...)
	}
	require.Len(t, rows, 3, "every interactive decision must leave a hitl_approvals row")
	states := map[runtimetypes.HITLApprovalState]int{}
	for _, r := range rows {
		states[r.State]++
		require.Equal(t, "local_shell", r.ToolsName)
		require.Equal(t, "exec", r.ToolName)
		require.Equal(t, "git push origin main", r.ArgsSummary)
		require.Equal(t, "hitl-policy-default.json", r.PolicyName)
		require.NotNil(t, r.MatchedRule)
		require.Equal(t, internalID, r.SessionID)
		require.NotNil(t, r.ResolvedAt)
		require.Contains(t, string(r.Resolution), "acp_client")
	}
	require.Equal(t, 1, states[runtimetypes.HITLApprovalApproved])
	require.Equal(t, 2, states[runtimetypes.HITLApprovalDenied], "a deny and a cancelled ask are both recorded as denied")
}
