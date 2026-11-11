package runtimetypes_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func eventDB(t *testing.T) (context.Context, libdb.DBManager) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "events.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db
}

func fixedClock(t time.Time) runtimetypes.EventStoreOption {
	return runtimetypes.WithEventClock(func() time.Time { return t })
}

func TestUnit_EventStore_AppendEventsWritesTheBatchInOrder(t *testing.T) {
	ctx, db := eventDB(t)
	store := runtimetypes.NewEventStore(db)
	batch := []*runtimetypes.Event{
		{WorkspaceID: "ws", Type: "fs.created", Subject: "a"},
		{WorkspaceID: "ws", Type: "fs.modified", Subject: "b"},
		{WorkspaceID: "ws", Type: "fs.deleted", Subject: "c"},
	}
	require.NoError(t, store.AppendEvents(ctx, batch))
	require.Equal(t, batch[0].NID+1, batch[1].NID)
	require.Equal(t, batch[1].NID+1, batch[2].NID)

	got, err := store.ListEventsSince(ctx, "ws", 0, 0)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"a", "b", "c"}, []string{got[0].Subject, got[1].Subject, got[2].Subject})
}

func TestUnit_EventStore_OneInvalidEventKeepsTheWholeBatchOut(t *testing.T) {
	ctx, db := eventDB(t)
	store := runtimetypes.NewEventStore(db)
	err := store.AppendEvents(ctx, []*runtimetypes.Event{
		{WorkspaceID: "ws", Type: "fs.created", Subject: "a"},
		{WorkspaceID: "ws", Type: " ", Subject: "b"},
	})
	require.ErrorIs(t, err, runtimetypes.ErrEventTypeRequired)
	got, err := store.ListEventsSince(ctx, "ws", 0, 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestUnit_EventStore_ADayPrunedHereIsRecreatedOnTheNextAppend(t *testing.T) {
	ctx, db := eventDB(t)
	day := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	store := runtimetypes.NewEventStore(db, fixedClock(day))
	require.NoError(t, store.AppendEvent(ctx, &runtimetypes.Event{WorkspaceID: "ws", Type: "fs.created"}))

	dropped, err := store.PruneEventPartitionsBefore(ctx, day.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, []string{"20261110"}, dropped)

	require.NoError(t, store.AppendEvent(ctx, &runtimetypes.Event{WorkspaceID: "ws", Type: "fs.modified"}))
	got, err := store.ListEventsSince(ctx, "ws", 0, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "fs.modified", got[0].Type)
}

func TestUnit_EventStore_ADayPrunedByAnotherProcessIsRecreatedOnTheNextAppend(t *testing.T) {
	ctx, db := eventDB(t)
	day := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	writer := runtimetypes.NewEventStore(db, fixedClock(day))
	pruner := runtimetypes.NewEventStore(db, fixedClock(day))
	require.NoError(t, writer.AppendEvent(ctx, &runtimetypes.Event{WorkspaceID: "ws", Type: "fs.created"}))

	_, err := pruner.PruneEventPartitionsBefore(ctx, day.Add(24*time.Hour))
	require.NoError(t, err)

	require.NoError(t, writer.AppendEvent(ctx, &runtimetypes.Event{WorkspaceID: "ws", Type: "fs.modified"}))
	parts, err := writer.ListEventPartitions(ctx)
	require.NoError(t, err)
	require.Len(t, parts, 1)
}

func TestUnit_EventStore_PruneKeepsTheDaysInsideTheWindow(t *testing.T) {
	ctx, db := eventDB(t)
	day := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, offset := range []int{-40, -31, -30, -1, 0} {
		ts := day.AddDate(0, 0, offset)
		store := runtimetypes.NewEventStore(db, fixedClock(ts))
		require.NoError(t, store.AppendEvent(ctx, &runtimetypes.Event{WorkspaceID: "ws", Type: "fs.created"}))
	}
	store := runtimetypes.NewEventStore(db, fixedClock(day))
	dropped, err := store.PruneEventPartitionsBefore(ctx, day.Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, []string{"20261001", "20261010"}, dropped)
	parts, err := store.ListEventPartitions(ctx)
	require.NoError(t, err)
	require.Len(t, parts, 3)
}
