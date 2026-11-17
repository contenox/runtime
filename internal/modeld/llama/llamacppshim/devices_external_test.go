package llamacppshim_test

import (
	"reflect"
	"testing"

	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/llama/llamacppshim"
	"github.com/contenox/runtime/internal/transport"
)

func TestUnit_PhysicalGPUViewsResolveOneNativeDevice(t *testing.T) {
	first := llamacppshim.Device{ID: "pci:0000:01:00.0", Index: 0, Name: "CUDA0", Type: "gpu", MemoryTotal: 6 << 30}
	second := llamacppshim.Device{ID: "pci:0000:02:00.0", Index: 1, Name: "CUDA1", Type: "gpu", MemoryTotal: 8 << 30}
	duplicate := llamacppshim.Device{ID: first.ID, Index: 3, Name: "Vulkan1", Type: "gpu", MemoryTotal: 7 << 30}
	for _, order := range [][]llamacppshim.Device{{first, second, duplicate}, {duplicate, second, first}} {
		devices := llamacppshim.DeduplicateDevices(order)
		want := order[:2]
		if !reflect.DeepEqual(devices, want) {
			t.Fatalf("physical devices = %+v, want %+v", devices, want)
		}
		inventory := make([]transport.DeviceInfo, len(devices))
		for index, device := range devices {
			inventory[index] = transport.DeviceInfo{ID: device.ID, Index: device.Index, Name: device.Name, Type: device.Type, MemoryTotal: int64(device.MemoryTotal)}
		}
		for _, device := range inventory {
			resolved, err := llama.AllocationDevices(transport.Config{NumGpuLayers: 20, Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: device.ID}}, inventory)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(resolved, []transport.DeviceInfo{device}) {
				t.Fatalf("allocation = %+v, want native device %+v", resolved, device)
			}
		}
	}
}

func TestUnit_PhysicalDeviceDeduplicationPreservesUnidentifiedAndHostDevices(t *testing.T) {
	devices := []llamacppshim.Device{
		{Index: 0, Name: "GPU0", Type: "gpu"},
		{Index: 1, Name: "GPU1", Type: "gpu"},
		{ID: "host", Index: 2, Name: "CPU", Type: "cpu"},
		{ID: "host", Index: 3, Name: "BLAS", Type: "accel"},
		{ID: "pci:0000:00:02.0", Index: 4, Name: "Vulkan0", Type: "igpu"},
	}
	got := llamacppshim.DeduplicateDevices(devices)
	if !reflect.DeepEqual(got, devices) {
		t.Fatalf("devices = %+v, want %+v", got, devices)
	}
}
