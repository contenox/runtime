package admission_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/stretchr/testify/require"
)

func TestUnit_YieldAcknowledgesNativeBoundaryAndRetainsPermitAndMemory(t *testing.T) {
	scheduler := resourceScheduler(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, worker := range []string{"chat", "completion"} {
		allocation := memoryAllocation(worker, worker, 20)
		allocation.CanYield = true
		allocation.Pools = append(allocation.Pools, admission.PoolAllocation{PoolID: "gpu", Bytes: 30, LimitBytes: 100, AvailableBytes: 100})
		require.NoError(t, scheduler.Reserve(ctx, allocation))
	}
	chat, err := scheduler.Acquire(ctx, admission.Request{WorkerID: "chat", OwnerEpoch: "owner", Priority: admission.Foreground})
	require.NoError(t, err)
	type result struct {
		permit admission.Permit
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		permit, err := scheduler.Acquire(ctx, admission.Request{WorkerID: "completion", OwnerEpoch: "owner", Priority: admission.Autocomplete})
		completed <- result{permit, err}
	}()
	require.NoError(t, scheduler.WaitYield(ctx, chat))
	select {
	case result := <-completed:
		t.Fatalf("completion started before native acknowledged suspension: %+v", result)
	default:
	}
	resumed := make(chan error, 1)
	go func() { resumed <- scheduler.ResumeAtBoundary(ctx, chat) }()
	var completion result
	select {
	case completion = <-completed:
		require.NoError(t, completion.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	state, err := scheduler.Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, state.Active)
	require.True(t, state.Workers[0].Suspended)
	require.Equal(t, chat, *state.Workers[0].Active)
	require.Len(t, state.Resources.Allocations, 2)
	blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = scheduler.Acquire(blocked, admission.Request{WorkerID: "chat", OwnerEpoch: "owner", Priority: admission.Autocomplete})
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, scheduler.Release(ctx, completion.permit))
	select {
	case err := <-resumed:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	state, err = scheduler.Snapshot(ctx)
	require.NoError(t, err)
	require.False(t, state.Workers[0].Suspended)
	require.Equal(t, chat, *state.Workers[0].Active)
	require.NoError(t, scheduler.Release(ctx, chat))
}

func TestUnit_UnsupportedYieldKeepsExecutionSerialized(t *testing.T) {
	scheduler := resourceScheduler(t)
	for _, worker := range []string{"chat", "completion"} {
		allocation := memoryAllocation(worker, worker, 20)
		allocation.Threads = allocation.ThreadLimit
		require.NoError(t, scheduler.Reserve(t.Context(), allocation))
	}
	chat, err := scheduler.Acquire(t.Context(), admission.Request{WorkerID: "chat", OwnerEpoch: "owner", Priority: admission.Foreground})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = scheduler.Acquire(ctx, admission.Request{WorkerID: "completion", OwnerEpoch: "owner", Priority: admission.Autocomplete})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	state, err := scheduler.Snapshot(t.Context())
	require.NoError(t, err)
	require.False(t, state.Workers[0].Suspended)
	require.False(t, state.Workers[0].YieldRequested)
	require.Equal(t, chat, *state.Workers[0].Active)
	require.NoError(t, scheduler.Release(t.Context(), chat))
}
