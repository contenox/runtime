package operatorinbox

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func setupInboxDB(t *testing.T) (context.Context, libdb.DBManager) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "operatorinbox.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db
}

func TestUnit_Inbox_AddAndListRoundtrip(t *testing.T) {
	ctx, db := setupInboxDB(t)
	svc := New(db)

	item := &Item{
		MissionID: "m1",
		AgentName: "runner",
		Intent:    "do the thing",
		Reason:    ReasonOperatorFired,
		Report:    missionservice.Report{ID: "r1", MissionID: "m1", Kind: missionservice.ReportKindResult, Summary: "done"},
	}
	require.NoError(t, svc.Add(ctx, item))
	require.NotEmpty(t, item.ID, "Add assigns an id when absent")
	require.False(t, item.CreatedAt.IsZero(), "Add stamps CreatedAt when absent")

	items, err := svc.List(ctx, 100)
	require.NoError(t, err)
	require.Len(t, items, 1)
	got := items[0]
	require.Equal(t, item.ID, got.ID)
	require.Equal(t, "m1", got.MissionID)
	require.Equal(t, "runner", got.AgentName)
	require.Equal(t, ReasonOperatorFired, got.Reason)
	require.Equal(t, "done", got.Report.Summary)
	require.Equal(t, missionservice.ReportKindResult, got.Report.Kind)
}

func TestUnit_Inbox_ListNewestFirst(t *testing.T) {
	ctx, db := setupInboxDB(t)
	svc := New(db)

	require.NoError(t, svc.Add(ctx, &Item{MissionID: "m1", Reason: ReasonOperatorFired,
		Report: missionservice.Report{Kind: missionservice.ReportKindProgress, Summary: "first"}}))
	require.NoError(t, svc.Add(ctx, &Item{MissionID: "m2", Reason: ReasonParentGone, ParentSessionID: "gone",
		Report: missionservice.Report{Kind: missionservice.ReportKindResult, Summary: "second"}}))

	items, err := svc.List(ctx, 100)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "second", items[0].Report.Summary, "newest first")
	require.Equal(t, "first", items[1].Report.Summary)
	require.Equal(t, ReasonParentGone, items[0].Reason)
	require.Equal(t, "gone", items[0].ParentSessionID)
}

func TestUnit_Inbox_EmptyIsNonNil(t *testing.T) {
	ctx, db := setupInboxDB(t)
	svc := New(db)
	items, err := svc.List(ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, items)
	require.Empty(t, items)
}

func TestUnit_Inbox_AddValidates(t *testing.T) {
	ctx, db := setupInboxDB(t)
	svc := New(db)

	require.Error(t, svc.Add(ctx, &Item{Reason: ReasonOperatorFired}), "missionId is required")
	require.Error(t, svc.Add(ctx, &Item{MissionID: "m1", Reason: "bogus"}), "reason must be a known value")
	require.Error(t, svc.Add(ctx, nil))
}

func TestUnit_Inbox_AddWithSameIDIsIdempotentAndKeepsFirstWrite(t *testing.T) {
	ctx, db := setupInboxDB(t)
	svc := New(db)
	id := ItemIDForReport("m1", "r1")

	first := &Item{ID: id, MissionID: "m1", Reason: ReasonOperatorFired,
		Report: missionservice.Report{ID: "r1", Summary: "done"}}
	require.NoError(t, svc.Add(ctx, first))
	second := &Item{ID: id, MissionID: "m1", Reason: ReasonParentGone,
		Report: missionservice.Report{ID: "r1", Summary: "done"}}
	require.NoError(t, svc.Add(ctx, second))

	items, err := svc.List(ctx, 100)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ReasonOperatorFired, items[0].Reason, "first write wins")
	require.True(t, first.CreatedAt.Equal(items[0].CreatedAt), "CreatedAt not clobbered")
}
