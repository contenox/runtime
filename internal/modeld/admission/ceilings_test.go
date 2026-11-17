package admission_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/stretchr/testify/require"
)

func gpuAllocation(worker, identity string, bytes int64) admission.Allocation {
	allocation := memoryAllocation(worker, identity, 1)
	allocation.Pools = append(allocation.Pools, admission.PoolAllocation{PoolID: "gpu:first", Bytes: bytes, LimitBytes: 90, AvailableBytes: 100, TotalBytes: 100})
	return allocation
}

func TestUnit_DeviceCeilingRefusesAnAllocationAboveItByName(t *testing.T) {
	scheduler := resourceScheduler(t)
	require.NoError(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{"gpu:first": 0.6}))
	err := scheduler.Reserve(t.Context(), gpuAllocation("chat", "chat", 70))
	require.ErrorIs(t, err, admission.ErrCapacity)
	require.ErrorContains(t, err, "pool gpu:first")
	require.ErrorContains(t, err, "device ceiling of 60%")
	err = scheduler.Prepare(t.Context(), gpuAllocation("chat", "chat", 70))
	require.ErrorIs(t, err, admission.ErrCapacity)
	require.ErrorContains(t, err, "device ceiling of 60%")
	require.NoError(t, scheduler.Reserve(t.Context(), gpuAllocation("chat", "chat", 60)))
	snapshot, err := scheduler.Snapshot(t.Context())
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"gpu:first": 0.6}, snapshot.Resources.Ceilings)
}

func TestUnit_DeviceCeilingBoundsTheSumOfAPoolsAllocations(t *testing.T) {
	scheduler := resourceScheduler(t)
	require.NoError(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{"gpu:first": 0.5}))
	require.NoError(t, scheduler.Reserve(t.Context(), gpuAllocation("chat", "chat", 30)))
	err := scheduler.Reserve(t.Context(), gpuAllocation("embedding", "embedding", 30))
	require.ErrorIs(t, err, admission.ErrCapacity)
	require.ErrorContains(t, err, "device ceiling of 50%")
	require.ErrorContains(t, err, "raise the ceiling for gpu:first")
	err = scheduler.ConfigureCeilings(t.Context(), map[string]float64{"gpu:first": 0.2})
	require.ErrorIs(t, err, admission.ErrCapacity, "a ceiling the resident workers already exceed is refused")
	require.NoError(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{}))
	require.NoError(t, scheduler.Reserve(t.Context(), gpuAllocation("embedding", "embedding", 30)))
}

func TestUnit_DeviceCeilingRefusesAPoolWithoutTotalAndInvalidShares(t *testing.T) {
	scheduler := resourceScheduler(t)
	require.Error(t, scheduler.ConfigureCeilings(t.Context(), nil))
	require.Error(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{"gpu:first": 0}))
	require.Error(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{"gpu:first": 1.5}))
	require.NoError(t, scheduler.ConfigureCeilings(t.Context(), map[string]float64{"host": 0.9}))
	err := scheduler.Reserve(t.Context(), memoryAllocation("chat", "chat", 10))
	require.ErrorIs(t, err, admission.ErrCapacity)
	require.ErrorContains(t, err, "reports no total memory")
}
