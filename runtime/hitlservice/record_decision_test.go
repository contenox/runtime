package hitlservice_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_RecordDecision_WritesAResolvedRow(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	rec, ok := newDurableService(t, store).(hitlservice.DecisionRecorder)
	require.True(t, ok, "the durable approval service must be a DecisionRecorder")

	rule := 2
	asked := time.Now().UTC().Add(-3 * time.Second)
	cases := []struct {
		outcome      hitlservice.DecisionOutcome
		onTimeout    hitlservice.Action
		wantState    runtimetypes.HITLApprovalState
		wantApproved bool
	}{
		{hitlservice.DecisionApproved, "", runtimetypes.HITLApprovalApproved, true},
		{hitlservice.DecisionDenied, "", runtimetypes.HITLApprovalDenied, false},
		{hitlservice.DecisionTimedOut, hitlservice.ActionDeny, runtimetypes.HITLApprovalExpired, false},
		{hitlservice.DecisionTimedOut, hitlservice.ActionAllow, runtimetypes.HITLApprovalExpired, true},
		{hitlservice.DecisionCancelled, "", runtimetypes.HITLApprovalDenied, false},
	}
	for _, c := range cases {
		require.NoError(t, rec.RecordDecision(ctx, hitlservice.ApprovalRequest{
			ToolsName:   "local_shell",
			ToolName:    "exec",
			Args:        map[string]any{"command": "git push", "env": map[string]any{"TOKEN": "s3cr3t"}},
			PolicyName:  "hitl-policy-default.json",
			MatchedRule: &rule,
			OnTimeout:   c.onTimeout,
			SessionID:   "sess-1",
		}, hitlservice.Decision{Outcome: c.outcome, DecidedBy: "acp_client", AskedAt: asked}))
	}

	var got []*runtimetypes.HITLApproval
	for _, st := range []runtimetypes.HITLApprovalState{runtimetypes.HITLApprovalApproved, runtimetypes.HITLApprovalDenied, runtimetypes.HITLApprovalExpired} {
		rows, err := store.ListHITLApprovals(ctx, st, nil, 50)
		require.NoError(t, err)
		got = append(got, rows...)
	}
	require.Len(t, got, len(cases))

	byKey := map[string]*runtimetypes.HITLApproval{}
	for _, r := range got {
		require.Equal(t, "local_shell", r.ToolsName)
		require.Equal(t, "exec", r.ToolName)
		require.Equal(t, "git push", r.ArgsSummary)
		require.NotContains(t, r.ArgsSummary, "s3cr3t")
		require.Equal(t, "hitl-policy-default.json", r.PolicyName)
		require.NotNil(t, r.MatchedRule)
		require.Equal(t, 2, *r.MatchedRule)
		require.Equal(t, "sess-1", r.SessionID)
		require.NotNil(t, r.ResolvedAt)
		require.WithinDuration(t, asked, r.CreatedAt, time.Second)
		require.NotContains(t, string(r.Resolution), "s3cr3t")
		require.Contains(t, string(r.Resolution), "acp_client")
		byKey[string(r.State)+decodeKey(t, r)] = r
	}
	for _, c := range cases {
		found := false
		for _, r := range got {
			if r.State == c.wantState && decodeApproved(t, r.Resolution) == c.wantApproved {
				found = true
			}
		}
		require.True(t, found, "no row for outcome %s (state %s approved %v)", c.outcome, c.wantState, c.wantApproved)
	}
	require.Len(t, byKey, 4, "approved, denied, expired(deny), expired(allow) rows are distinguishable")
}

func decodeKey(t *testing.T, r *runtimetypes.HITLApproval) string {
	t.Helper()
	if decodeApproved(t, r.Resolution) {
		return "+"
	}
	return "-"
}

func TestUnit_RecordDecision_ResolvedRowsAreNotSweptOrListedPending(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)
	require.NoError(t, svc.(hitlservice.DecisionRecorder).RecordDecision(ctx,
		hitlservice.ApprovalRequest{ToolsName: "local_fs", ToolName: "write_file", TimeoutS: 1},
		hitlservice.Decision{Outcome: hitlservice.DecisionApproved, AskedAt: time.Now().Add(-time.Minute)}))

	pending, err := svc.ListPending(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, pending)
	n, err := svc.SweepExpired(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}
