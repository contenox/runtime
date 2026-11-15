package acpsvc

import (
	"fmt"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestContextShiftReplayAcrossPages(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	_, err := db.WithoutTransaction().ExecContext(ctx, "INSERT INTO message_indices (id,identity) VALUES ('internal','acp')")
	require.NoError(t, err)
	sess := &sessionEntry{InternalSessionID: "internal"}
	tr := &Transport{deps: Deps{DB: db}, sessions: map[libacp.SessionID]*sessionEntry{"chat": sess}}
	stamp := time.Now().UTC()
	for i := 0; i < 205; i++ {
		ev := taskengine.TaskEvent{Kind: taskengine.TaskEventContextShift, Timestamp: stamp, RequestID: fmt.Sprintf("%03d", i), ContextShift: &taskengine.ContextShift{Window: 1000000, Kept: 2, Removed: 1}}
		tr.recordContextEvent(ctx, sess, ev)
		if i == 99 {
			tr.recordContextEvent(ctx, sess, ev)
		}
	}
	sess.setEffectiveTokenLimit(65536)
	count := 0
	for ev := range tr.contextShiftHistory(ctx, "chat") {
		require.Equal(t, fmt.Sprintf("%03d", count), ev.RequestID)
		require.Equal(t, 1000000, ev.ContextShift.Window)
		count++
	}
	require.Equal(t, 205, count)
}
