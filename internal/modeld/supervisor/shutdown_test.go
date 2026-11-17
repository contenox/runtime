package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_ShutdownPreservesOtherClientUntilItsSessionCloses(t *testing.T) {
	fixture := sharedOwner(t)
	client, control := connectOwner(t, fixture), connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	session, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	request := transport.OwnerShutdownRequest{Fence: transport.Fence{OwnerInstanceID: fixture.holder.InstanceID()}}
	require.ErrorIs(t, control.ShutdownOwner(ctx, request), transport.ErrModelBusy)
	_, err = session.EnsurePrefix(ctx, transport.PrefixInput{Text: "the other client's conversation survives"})
	require.NoError(t, err)
	require.False(t, session.ExplainContext().Closed)
	stale := request
	stale.OwnerInstanceID = "expired-owner"
	require.ErrorIs(t, control.ShutdownOwner(ctx, stale), transport.ErrStaleFence)
	require.NoError(t, session.Close())
	require.NoError(t, control.ShutdownOwner(ctx, request))
	require.NoError(t, modeldconn.WaitForPreviousWorkers(ctx, fixture.root, "successor"))
	scheduling, err := fixture.pool.Scheduler().Snapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, scheduling.Resources.Allocations)
	for _, worker := range scheduling.Workers {
		require.True(t, worker.Stopped)
		require.Nil(t, worker.Active)
	}
	_, _, err = fixture.pool.Acquire(ctx, modeldconn.WorkerSpec{Role: "primary", Model: "fixture", Backend: "llama"})
	require.ErrorContains(t, err, "closed")
}
