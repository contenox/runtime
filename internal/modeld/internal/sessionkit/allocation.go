package sessionkit

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

// Allocation accounts for host storage and each resolved accelerator pool without
// counting shared host/device memory twice. Device capacity and runtime overhead
// come from the backend resolver; multi-device weights use the reviewed proportions.
func Allocation(cfg transport.Config, info transport.ModelInfo, weights int64, devices []transport.DeviceInfo, host capacity.DeviceSnapshot) (transport.NativeAllocation, error) {
	if host.TotalBytes <= 0 || host.FreeBytes < 0 || weights <= 0 || info.RequiredBytes <= 0 {
		return transport.NativeAllocation{}, errors.New("native allocation requires model size, resolved capacity and host memory telemetry")
	}
	threads := cfg.NumThreads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	if threads > runtime.NumCPU() {
		return transport.NativeAllocation{}, errors.New("native thread allocation exceeds host capacity; " + remedy.ReplanPlacement)
	}
	cfg.NumThreads = threads
	result := transport.NativeAllocation{Devices: devices, Config: cfg, Resources: admission.Allocation{Threads: threads, ThreadLimit: runtime.NumCPU()}}
	pools := map[string]admission.PoolAllocation{}
	cold := min(max(info.HostColdBudgetBytes, 0), int64(max(cfg.PlannerEffectiveContext-cfg.NumCtx, 0))*max(info.KVBytesPerToken, 0))
	hostBytes := cold + capacity.NativeHostOverheadBytes
	if len(devices) == 0 {
		hostBytes += info.RequiredBytes
	} else {
		hostBytes += max(weights-info.WeightsBytes, 0)
	}
	pools["host"] = admission.PoolAllocation{PoolID: "host", Bytes: hostBytes, LimitBytes: max(host.TotalBytes-info.MinFreeBytes, 0), AvailableBytes: host.FreeBytes, TotalBytes: host.TotalBytes}
	shares := make([]float64, len(devices))
	reviewed := []float64{}
	for _, share := range cfg.TensorSplit {
		if share > 0 {
			reviewed = append(reviewed, float64(share))
		}
	}
	totalShare := float64(0)
	for index := range devices {
		share := float64(1)
		if len(cfg.TensorSplit) > 0 {
			if len(reviewed) != len(devices) {
				return result, errors.New("resolved device has no reviewed allocation proportion")
			}
			share = reviewed[index]
		}
		if share <= 0 || math.IsNaN(share) || math.IsInf(share, 0) {
			return result, errors.New("invalid device allocation proportion")
		}
		shares[index] = share
		totalShare += share
	}
	for index, device := range devices {
		poolID := device.PoolID
		if device.SharedMemory {
			poolID = "host"
		} else if poolID == "" {
			poolID = device.ID
		}
		bytes := int64(math.Ceil(float64(max(info.RequiredBytes-info.OverheadBytes, 0))*shares[index]/totalShare)) + info.OverheadBytes
		if poolID == "host" {
			pool := pools["host"]
			pool.Bytes += bytes
			pools["host"] = pool
			continue
		}
		if poolID == "" || !device.MemoryTotalKnown || !device.MemoryFreeKnown || device.MemoryTotal <= 0 {
			return result, fmt.Errorf("accelerator %s has incomplete memory telemetry; "+remedy.ReplanPlacement, device.ID)
		}
		pool := admission.PoolAllocation{PoolID: poolID, Bytes: bytes, LimitBytes: max(device.MemoryTotal-info.MinFreeBytes, 0), AvailableBytes: device.MemoryFree, TotalBytes: device.MemoryTotal}
		if previous, exists := pools[poolID]; exists {
			pool.Bytes += previous.Bytes
			pool.LimitBytes = min(pool.LimitBytes, previous.LimitBytes)
			pool.AvailableBytes = min(pool.AvailableBytes, previous.AvailableBytes)
			pool.TotalBytes = min(pool.TotalBytes, previous.TotalBytes)
		}
		pools[poolID] = pool
	}
	for _, pool := range pools {
		result.Resources.Pools = append(result.Resources.Pools, pool)
	}
	sort.Slice(result.Resources.Pools, func(i, j int) bool { return result.Resources.Pools[i].PoolID < result.Resources.Pools[j].PoolID })
	return result, nil
}
