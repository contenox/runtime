package hitlservice_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_ListPending_ReturnsOnlyPendingNewestFirst(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	base := time.Now().UTC().Add(-time.Hour)
	var pendingIDs []string
	for i := 0; i < 3; i++ {
		row := seedPendingRow(t, ctx, store, "deny", base.Add(time.Duration(i)*time.Minute), base.Add(time.Hour))
		pendingIDs = append(pendingIDs, row.ID)
	}

	resolved := seedPendingRow(t, ctx, store, "deny", base, base.Add(time.Hour))
	require.NoError(t, svc.Respond(ctx, resolved.ID, true))

	got, err := svc.ListPending(ctx, 100)
	require.NoError(t, err)
	require.Len(t, got, 3, "the resolved row must not appear in the pending inbox")

	require.Equal(t, pendingIDs[2], got[0].ID)
	require.Equal(t, pendingIDs[1], got[1].ID)
	require.Equal(t, pendingIDs[0], got[2].ID)

	for _, row := range got {
		require.Equal(t, runtimetypes.HITLApprovalPending, row.State)
	}
}

func TestUnit_ListPending_CarriesTheDecisionFields(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	now := time.Now().UTC()
	row := &runtimetypes.HITLApproval{
		ID:          "decision-fields",
		ToolsName:   "local_fs",
		ToolName:    "write_file",
		ArgsSummary: "/workspace/main.go",
		PolicyName:  "hitl-policy-default.json",
		OnTimeout:   "deny",
		State:       runtimetypes.HITLApprovalPending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	diff := "--- a\n+++ b\n"
	row.Diff = &diff
	rule := 3
	row.MatchedRule = &rule
	require.NoError(t, store.CreateHITLApproval(ctx, row))

	got, err := svc.ListPending(ctx, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)

	gotRow := got[0]
	require.Equal(t, "local_fs", gotRow.ToolsName)
	require.Equal(t, "write_file", gotRow.ToolName)
	require.Equal(t, "/workspace/main.go", gotRow.ArgsSummary)
	require.NotNil(t, gotRow.Diff)
	require.Equal(t, diff, *gotRow.Diff)
	require.Equal(t, "hitl-policy-default.json", gotRow.PolicyName)
	require.NotNil(t, gotRow.MatchedRule)
	require.Equal(t, 3, *gotRow.MatchedRule)
	require.WithinDuration(t, row.CreatedAt, gotRow.CreatedAt, time.Second)
	require.WithinDuration(t, row.ExpiresAt, gotRow.ExpiresAt, time.Second)
}

func TestUnit_ListPending_RespectsLimit(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		seedPendingRow(t, ctx, store, "deny", base.Add(time.Duration(i)*time.Minute), base.Add(time.Hour))
	}

	got, err := svc.ListPending(ctx, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestUnit_ListPending_EmptyIsNonNil(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	got, err := svc.ListPending(ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestUnit_ListPending_ZeroLimitUsesStoreDefault(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	seedPendingRow(t, ctx, store, "deny", time.Now().UTC(), time.Now().UTC().Add(time.Hour))

	got, err := svc.ListPending(ctx, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestUnit_ListPending_WithoutDurableStoreErrors(t *testing.T) {
	t.Parallel()
	svc := hitlservice.New(hitlservice.NewFSPolicySource(t.TempDir()), testTenant, fixedKVReader{"hitl-policy.json"}, libtracker.NoopTracker{})

	_, err := svc.ListPending(t.Context(), 100)
	require.Error(t, err)
}
