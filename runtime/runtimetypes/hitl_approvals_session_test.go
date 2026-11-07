package runtimetypes_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_HITLApprovals_ToolCallIDRoundTripsAndListsBySession(t *testing.T) {
	t.Parallel()
	ctx, s := setupHITLApprovalsStore(t)

	first := newPendingApproval()
	first.SessionID = "session-a"
	first.ToolCallID = "call-1"
	require.NoError(t, s.CreateHITLApproval(ctx, first))

	second := newPendingApproval()
	second.SessionID = "session-a"
	second.ToolCallID = "call-2"
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	require.NoError(t, s.CreateHITLApproval(ctx, second))
	require.NoError(t, s.ResolveHITLApproval(ctx, second.ID, runtimetypes.HITLApprovalApproved, json.RawMessage(`{"approved":true,"decidedBy":"inbox"}`), time.Now().UTC()))

	other := newPendingApproval()
	other.SessionID = "session-b"
	other.ToolCallID = "call-3"
	require.NoError(t, s.CreateHITLApproval(ctx, other))

	got, err := s.GetHITLApproval(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "call-1", got.ToolCallID)

	rows, err := s.ListHITLApprovalsForSession(ctx, "session-a", 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "call-2", rows[0].ToolCallID, "newest first")
	require.Equal(t, runtimetypes.HITLApprovalApproved, rows[0].State)
	require.Equal(t, "call-1", rows[1].ToolCallID)

	none, err := s.ListHITLApprovalsForSession(ctx, "session-c", 0)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestUnit_HITLApprovals_LegacyRowsHaveNoToolCallID(t *testing.T) {
	t.Parallel()
	ctx, s := setupHITLApprovalsStore(t)

	row := newPendingApproval()
	require.NoError(t, s.CreateHITLApproval(ctx, row))
	got, err := s.GetHITLApproval(ctx, row.ID)
	require.NoError(t, err)
	require.Empty(t, got.ToolCallID)
}
