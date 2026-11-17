package llama_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_AllocationDevicesUsesResolvedPlacementAndEverySplitDevice(t *testing.T) {
	devices := []transport.DeviceInfo{{ID: "cpu", Type: "cpu"}, {ID: "pci:first", Type: "gpu"}, {ID: "pci:second", Type: "gpu"}}
	for _, test := range []struct {
		name   string
		config transport.Config
		want   []transport.DeviceInfo
		fails  bool
	}{
		{name: "explicit CPU", config: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}}, want: []transport.DeviceInfo{}},
		{name: "physical device", config: transport.Config{NumGpuLayers: 20, Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "pci:second"}}, want: devices[2:]},
		{name: "multi device", config: transport.Config{NumGpuLayers: 20, TensorSplit: []float32{1, 2}}, want: devices[1:]},
		{name: "missing device", config: transport.Config{NumGpuLayers: 20, TensorSplit: []float32{1, 1, 1}}, fails: true},
		{name: "unresolved placement", config: transport.Config{NumGpuLayers: 20}, fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := llama.AllocationDevices(test.config, devices)
			if test.fails {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, resolved)
		})
	}
}
