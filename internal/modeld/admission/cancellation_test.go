package admission_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Admission_ActiveSupersessionRetainsPermitUntilNativeStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "scheduler.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	policy := admission.Config{QueueCapacity: 2, ForegroundBurst: 2}
	scheduler, err := admission.NewEventScheduler(ctx, db, bus, policy, func(err error) { t.Error(err) })
	require.NoError(t, err)
	observer, err := admission.NewEventScheduler(ctx, db, bus, policy, func(err error) { t.Error(err) })
	require.NoError(t, err)
	require.NoError(t, scheduler.Register(ctx, admission.Worker{ID: "worker", OwnerEpoch: "epoch", Ready: true}))
	request := admission.Request{WorkerID: "worker", OwnerEpoch: "epoch", RequesterID: "client/editor", Priority: admission.Autocomplete, Supersede: true}
	active, err := scheduler.Acquire(ctx, request)
	require.NoError(t, err)
	type result struct {
		permit admission.Permit
		err    error
	}
	next := make(chan result, 1)
	go func() { permit, err := scheduler.Acquire(ctx, request); next <- result{permit, err} }()
	require.ErrorIs(t, observer.WaitCancellation(ctx, active), admission.ErrSuperseded)
	state, err := scheduler.Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, state.Active)
	require.Equal(t, active, *state.Workers[0].Active)
	require.True(t, state.Workers[0].CancellationRequested)
	require.Equal(t, 1, state.Workers[0].QueuedForeground)
	select {
	case granted := <-next:
		t.Fatalf("replacement admitted before native stop: %+v", granted)
	default:
	}
	require.NoError(t, scheduler.Release(ctx, active))
	select {
	case granted := <-next:
		require.NoError(t, granted.err)
		require.NotEqual(t, active.ID, granted.permit.ID)
		require.NoError(t, scheduler.Release(ctx, granted.permit))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, observer.WaitCancellation(ctx, active))
}
