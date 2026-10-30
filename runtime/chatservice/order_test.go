package chatservice_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/messagestore"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

// Messages persisted with identical timestamps must come back in the order they
// were appended, whatever precision the database keeps for the timestamp. The
// read order is (added_at, seq, id); seq is assigned in append order.
func TestUnit_PersistDiff_IdenticalTimestampsKeepAppendOrder(t *testing.T) {
	const n = 12
	// Whole second, no fraction: the worst case for a coarse clock.
	stamp := time.Date(2026, 10, 30, 11, 18, 45, 0, time.UTC)

	for run := 0; run < 10; run++ {
		t.Run(fmt.Sprintf("run-%d", run), func(t *testing.T) {
			ctx, db := setupDB(t)
			exec := db.WithoutTransaction()
			require.NoError(t, messagestore.New(exec, "").CreateMessageIndex(ctx, "sess", "alice"))
			mgr := chatservice.NewManager("")

			// First write: a batch of n messages that all share one timestamp.
			var hist []taskengine.Message
			for i := 0; i < n; i++ {
				hist = append(hist, taskengine.Message{Role: "user", Content: fmt.Sprintf("m-%02d", i), Timestamp: stamp})
			}
			require.NoError(t, mgr.PersistDiff(ctx, exec, "sess", hist))

			// Second write (a later turn): the same history plus more messages that
			// again carry the same timestamp.
			for i := n; i < 2*n; i++ {
				hist = append(hist, taskengine.Message{Role: "assistant", Content: fmt.Sprintf("m-%02d", i), Timestamp: stamp})
			}
			require.NoError(t, mgr.PersistDiff(ctx, exec, "sess", hist))

			assertContents(t, ctx, mgr, exec, 2*n)

			// The raw store agrees, and the timestamps were not rewritten.
			rows, err := messagestore.New(exec, "").ListMessages(ctx, "sess")
			require.NoError(t, err)
			require.Len(t, rows, 2*n)
			for i, r := range rows {
				require.True(t, r.AddedAt.Equal(stamp), "row %d added_at %v was altered", i, r.AddedAt)
				require.Equal(t, int64(i+1), r.Seq)
			}
		})
	}
}

// A message stamped before what is stored is clamped to the stream's latest
// timestamp instead of jumping ahead of earlier messages.
func TestUnit_PersistDiff_OlderTimestampDoesNotJumpTheQueue(t *testing.T) {
	ctx, db := setupDB(t)
	exec := db.WithoutTransaction()
	require.NoError(t, messagestore.New(exec, "").CreateMessageIndex(ctx, "sess", "alice"))
	mgr := chatservice.NewManager("")
	late := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	hist := []taskengine.Message{{Role: "user", Content: "m-00", Timestamp: late}}
	require.NoError(t, mgr.PersistDiff(ctx, exec, "sess", hist))
	hist = append(hist, taskengine.Message{Role: "assistant", Content: "m-01", Timestamp: late.Add(-time.Hour)})
	require.NoError(t, mgr.PersistDiff(ctx, exec, "sess", hist))

	assertContents(t, ctx, mgr, exec, 2)
}

func assertContents(t *testing.T, ctx context.Context, mgr *chatservice.Manager, exec libdb.Exec, want int) {
	t.Helper()
	got, err := mgr.ListMessages(ctx, exec, "sess")
	require.NoError(t, err)
	require.Len(t, got, want)
	for i, m := range got {
		require.Equal(t, fmt.Sprintf("m-%02d", i), m.Content, "message %d out of append order", i)
	}
}
