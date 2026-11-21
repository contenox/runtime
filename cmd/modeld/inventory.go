package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"sort"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

// A standalone daemon serves only its selected backend, even when its binary
// includes other engines. Report that same backend to catalog discovery.
type standaloneService struct {
	*slot.Service
	backend string
}

func (s standaloneService) Inventory(ctx context.Context) (transport.Inventory, error) {
	inventory, err := nativeInventory(ctx)
	if err != nil {
		return transport.Inventory{}, err
	}
	for _, backend := range inventory.Backends {
		if backend.Backend == s.backend {
			inventory.Backends = []transport.BackendInventory{backend}
			return inventory, nil
		}
	}
	inventory.Backends = nil
	return inventory, nil
}

func nativeInventory(ctx context.Context) (transport.Inventory, error) {
	if err := ctx.Err(); err != nil {
		return transport.Inventory{}, err
	}
	result := transport.Inventory{HostThreads: runtime.NumCPU(), Backends: []transport.BackendInventory{}, Pools: []transport.MemoryPool{}}
	host := transport.MemoryPool{ID: "host", Kind: "host", State: "available"}
	ram, err := capacity.SystemRAM{}.Snapshot()
	if err != nil {
		host.State, host.Error = "unavailable", err.Error()
	} else {
		host.TotalBytes, host.AvailableBytes = ram.TotalBytes, ram.FreeBytes
	}
	pools := map[string]transport.MemoryPool{"host": host}
	for _, name := range []string{"llama", "openvino"} {
		backend := transport.BackendInventory{Backend: name, State: "not-built", Operations: []string{}, Devices: []transport.DeviceInfo{}}
		if _, compiled := backends[name]; !compiled {
			backend.Error = "This worker was built without this backend; " + remedy.PrepareLocal + "."
			result.Backends = append(result.Backends, backend)
			continue
		}
		probe, exists := backendDiagnostics[name]
		if !exists {
			backend.State, backend.Error = "unavailable", "The installed backend cannot enumerate devices; update local inference."
			result.Backends = append(result.Backends, backend)
			continue
		}
		diagnostic := probe()
		backend.RuntimeDigest, backend.Error = diagnostic.RuntimeDigest, diagnostic.Error
		backend.State = "available"
		if backend.Error != "" {
			backend.State = "unavailable"
		}
		backend.Operations = []string{"chat", "autocomplete", "next-edit", "embedding", "vision"}
		if name == "openvino" && ovsession.GenAIAvailable {
			backend.Operations = append(backend.Operations, transport.ImageRole, transport.VideoRole)
		}
		backend.Scheduling = transport.SchedulingCapabilities{SingleActiveRequest: true, YieldOperations: []string{}, Limitations: []string{"A worker runs one request at a time. Roles sharing that worker execute serially."}}
		if name == "llama" {
			backend.Scheduling.CooperativeYield, backend.Scheduling.ThreadControl = true, true
			backend.Scheduling.YieldOperations = []string{"chat", "autocomplete", "next-edit", "vision"}
			backend.Scheduling.Limitations = append(backend.Scheduling.Limitations, "Chat yields between prefill batches and decoded tokens. Image encoding and embedding batches must finish their native operation before yielding.")
		} else {
			backend.Scheduling.Limitations = append(backend.Scheduling.Limitations, "The packaged backend cannot pause running generation or set a CPU thread limit. Review independent resources or serialized execution.")
		}
		for _, device := range diagnostic.Devices {
			device.State = "available"
			device.IdentityState = "unavailable"
			device.Operations = []string{}
			if device.ID != "" {
				device.IdentityState = "stable"
			} else {
				device.State, device.Error = "unavailable", "The native backend does not expose a physical identity for this device."
			}
			if device.Type == "cpu" || device.Type == "gpu" || device.Type == "igpu" {
				device.Operations = append(device.Operations, backend.Operations...)
			} else {
				device.State, device.Error = "unavailable", "The packaged execution paths do not support this device class."
			}
			if device.Type == "cpu" || device.SharedMemory {
				device.PoolID = "host"
				if host.State != "available" {
					device.State, device.Error = "unavailable", host.Error
				}
			} else if device.ID != "" {
				device.PoolID = device.ID
				pool := transport.MemoryPool{ID: device.ID, Kind: "accelerator", TotalBytes: device.MemoryTotal, AvailableBytes: device.MemoryFree, State: "available"}
				if !device.MemoryTotalKnown || !device.MemoryFreeKnown {
					pool.State, pool.Error = "unavailable", "The driver does not expose complete memory telemetry."
					device.State, device.Error = "unavailable", pool.Error
				}
				if previous, exists := pools[device.ID]; exists && previous.State == "available" {
					if pool.State == "available" {
						pool.TotalBytes, pool.AvailableBytes = min(pool.TotalBytes, previous.TotalBytes), min(pool.AvailableBytes, previous.AvailableBytes)
					} else {
						pool = previous
					}
				}
				pools[device.ID] = pool
			}
			backend.Devices = append(backend.Devices, device)
		}
		result.Backends = append(result.Backends, backend)
	}
	for _, pool := range pools {
		result.Pools = append(result.Pools, pool)
	}
	sort.Slice(result.Pools, func(i, j int) bool { return result.Pools[i].ID < result.Pools[j].ID })
	return result, nil
}

func printInventory() error {
	inventory, err := nativeInventory(context.Background())
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(inventory)
}
