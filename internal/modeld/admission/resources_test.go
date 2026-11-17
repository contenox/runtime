package admission_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func resourceScheduler(t *testing.T) *admission.EventScheduler {
	t.Helper()
	db, err := libdbexec.NewSQLiteDBManager(t.Context(), filepath.Join(t.TempDir(), "admission.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	bus := libbus.NewSQLiteWithOptions(db.WithoutTransaction(), libbus.SQLiteBusOptions{})
	t.Cleanup(func() {
		require.NoError(t, bus.Close())
		require.NoError(t, db.Close())
	})
	scheduler, err := admission.NewEventScheduler(t.Context(), db, bus, admission.DefaultConfig(), func(err error) { t.Error(err) })
	require.NoError(t, err)
	for _, id := range []string{"chat", "completion", "embedding"} {
		require.NoError(t, scheduler.Register(t.Context(), admission.Worker{ID: id, OwnerEpoch: "owner", Ready: true}))
	}
	return scheduler
}

func memoryAllocation(worker, identity string, bytes int64) admission.Allocation {
	return admission.Allocation{WorkerID: worker, OwnerEpoch: "owner", Identity: identity, Threads: 1, ThreadLimit: 4,
		Pools: []admission.PoolAllocation{{PoolID: "host", Bytes: bytes, LimitBytes: 100, AvailableBytes: 100}}}
}

func TestUnit_CompletionProtectionRetainsOtherClientsAndAccountsForExternalUse(t *testing.T) {
	scheduler := resourceScheduler(t)
	first := memoryAllocation("completion", "first", 30)
	second := memoryAllocation("completion", "second", 40)
	require.NoError(t, scheduler.ProtectClient(t.Context(), "ide-a", []admission.Protection{{Identity: first.Identity, Pools: first.Pools}}))
	require.NoError(t, scheduler.ProtectClient(t.Context(), "ide-b", []admission.Protection{{Identity: second.Identity, Pools: second.Pools}}))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "embedding", 40)), admission.ErrCapacity)
	require.NoError(t, scheduler.ProtectClient(t.Context(), "ide-a", []admission.Protection{}))
	embedding := memoryAllocation("embedding", "embedding", 40)
	embedding.Pools[0].AvailableBytes = 60
	require.ErrorIs(t, scheduler.Reserve(t.Context(), embedding), admission.ErrCapacity)
	embedding.Pools[0].AvailableBytes = 80
	require.NoError(t, scheduler.Reserve(t.Context(), embedding))
	snapshot, err := scheduler.Snapshot(t.Context())
	require.NoError(t, err)
	require.Len(t, snapshot.Resources.Protections, 1)
	require.Equal(t, "ide-b", snapshot.Resources.Protections[0].ClientID)
	require.Len(t, snapshot.Resources.Allocations, 1)
}

func TestUnit_ResourcesRetainUntilConfirmedStopAndFenceReplacement(t *testing.T) {
	scheduler := resourceScheduler(t)
	ctx, cancel := context.WithCancel(t.Context())
	permit, err := scheduler.Acquire(ctx, admission.Request{WorkerID: "chat", OwnerEpoch: "owner", Priority: admission.Foreground})
	require.NoError(t, err)
	require.NoError(t, scheduler.Reserve(ctx, memoryAllocation("chat", "shared-artifact", 70)))
	cancel()
	require.NoError(t, scheduler.Release(t.Context(), permit))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "shared-artifact", 40)), admission.ErrCapacity)
	require.ErrorIs(t, scheduler.Register(t.Context(), admission.Worker{ID: "chat", OwnerEpoch: "successor", Ready: true}), admission.ErrWorkerBusy)
	require.NoError(t, scheduler.Update(t.Context(), admission.Worker{ID: "chat", OwnerEpoch: "owner", Draining: true}))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "different-artifact", 40)), admission.ErrCapacity)
	require.NoError(t, scheduler.Update(t.Context(), admission.Worker{ID: "chat", OwnerEpoch: "owner", Draining: true, Stopped: true}))
	require.NoError(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "different-artifact", 40)))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("chat", "shared-artifact", 70)), admission.ErrOwnerEpoch)
}

func TestUnit_CompletionProtectionIsConsumedOnlyByMatchingResidentWorker(t *testing.T) {
	scheduler := resourceScheduler(t)
	completion := memoryAllocation("completion", "completion-route", 40)
	require.NoError(t, scheduler.Protect(t.Context(), []admission.Protection{{Identity: completion.Identity, Pools: completion.Pools}}))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "other-route", 70)), admission.ErrCapacity)
	require.NoError(t, scheduler.Reserve(t.Context(), memoryAllocation("chat", "chat-route", 60)))
	require.NoError(t, scheduler.Reserve(t.Context(), completion))
	require.NoError(t, scheduler.Reserve(t.Context(), completion))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", completion.Identity, 40)), admission.ErrCapacity)
	state, err := scheduler.Snapshot(t.Context())
	require.NoError(t, err)
	require.Len(t, state.Resources.Allocations, 2)
	require.Len(t, state.Resources.Protections, 1)
	require.NoError(t, scheduler.Update(t.Context(), admission.Worker{ID: "completion", OwnerEpoch: "owner", Stopped: true}))
	require.ErrorIs(t, scheduler.Reserve(t.Context(), memoryAllocation("embedding", "other-route", 1)), admission.ErrCapacity)
}

func TestUnit_ResourcePoolsAreIndependentAndUnifiedMemoryIsNotRepeated(t *testing.T) {
	scheduler := resourceScheduler(t)
	chat := memoryAllocation("chat", "chat", 20)
	chat.Pools = append(chat.Pools, admission.PoolAllocation{PoolID: "gpu:first", Bytes: 80, LimitBytes: 100, AvailableBytes: 100})
	embed := memoryAllocation("embedding", "embedding", 20)
	embed.Pools = append(embed.Pools, admission.PoolAllocation{PoolID: "gpu:second", Bytes: 80, LimitBytes: 100, AvailableBytes: 100})
	require.NoError(t, scheduler.Reserve(t.Context(), chat))
	require.NoError(t, scheduler.Reserve(t.Context(), embed))
	completion := memoryAllocation("completion", "completion", 60)
	require.NoError(t, scheduler.Reserve(t.Context(), completion))
	completion.Pools = append(completion.Pools, completion.Pools[0])
	require.Error(t, scheduler.Reserve(t.Context(), completion))
	embed.Pools[1].Bytes = 101
	require.ErrorIs(t, scheduler.Reserve(t.Context(), embed), admission.ErrCapacity)
}

func TestUnit_ResourceObservationAndReservationCannotInventFreeMemory(t *testing.T) {
	scheduler := resourceScheduler(t)
	allocation := memoryAllocation("chat", "route", 70)
	allocation.Pools[0].AvailableBytes = 60
	require.ErrorIs(t, scheduler.Reserve(t.Context(), allocation), admission.ErrCapacity)
	allocation.Pools[0].AvailableBytes = 70
	require.NoError(t, scheduler.Reserve(t.Context(), allocation))
	allocation.Pools[0].AvailableBytes = 0
	require.NoError(t, scheduler.Reserve(t.Context(), allocation))
	allocation.Pools[0].Bytes = 60
	require.Error(t, scheduler.Reserve(t.Context(), allocation))
}
