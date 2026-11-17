package sessionkit_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_AllocationCountsUnifiedMemoryOnce(t *testing.T) {
	info := transport.ModelInfo{WeightsBytes: 400, RequiredBytes: 600, HostColdBudgetBytes: 80, KVBytesPerToken: 2}
	cfg := transport.Config{NumThreads: 1, NumCtx: 100, PlannerEffectiveContext: 140}
	device := transport.DeviceInfo{ID: "igpu", Type: "igpu", SharedMemory: true}
	allocation, err := sessionkit.Allocation(cfg, info, 400, []transport.DeviceInfo{device}, capacity.DeviceSnapshot{TotalBytes: 1 << 30, FreeBytes: 1 << 30})
	require.NoError(t, err)
	require.Len(t, allocation.Resources.Pools, 1)
	require.Equal(t, "host", allocation.Resources.Pools[0].PoolID)
	require.Equal(t, int64(680)+capacity.NativeHostOverheadBytes, allocation.Resources.Pools[0].Bytes)
}

func TestUnit_AllocationReportsEverySplitPoolAndHostColdStorage(t *testing.T) {
	devices := []transport.DeviceInfo{
		{ID: "first", Index: 0, Type: "gpu", MemoryTotal: 1000, MemoryFree: 1000, MemoryTotalKnown: true, MemoryFreeKnown: true},
		{ID: "second", Index: 1, Type: "gpu", MemoryTotal: 1000, MemoryFree: 1000, MemoryTotalKnown: true, MemoryFreeKnown: true},
	}
	info := transport.ModelInfo{WeightsBytes: 300, RequiredBytes: 700, OverheadBytes: 100, HostColdBudgetBytes: 80, KVBytesPerToken: 2}
	cfg := transport.Config{NumThreads: 1, NumCtx: 100, PlannerEffectiveContext: 140, TensorSplit: []float32{1, 2}}
	allocation, err := sessionkit.Allocation(cfg, info, 400, devices, capacity.DeviceSnapshot{TotalBytes: 1 << 30, FreeBytes: 1 << 30})
	require.NoError(t, err)
	bytes := map[string]int64{}
	for _, pool := range allocation.Resources.Pools {
		bytes[pool.PoolID] = pool.Bytes
	}
	require.Equal(t, map[string]int64{"first": 300, "second": 500, "host": 180 + capacity.NativeHostOverheadBytes}, bytes)
	devices[1].MemoryFreeKnown = false
	_, err = sessionkit.Allocation(cfg, info, 400, devices, capacity.DeviceSnapshot{TotalBytes: 1 << 30, FreeBytes: 1 << 30})
	require.ErrorContains(t, err, "incomplete memory telemetry")
}
