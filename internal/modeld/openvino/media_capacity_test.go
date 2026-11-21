package openvino

import (
	"context"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

type recordedMediaClaim struct {
	alloc transport.NativeAllocation
}

func (c *recordedMediaClaim) ClaimDevices(_ context.Context, allocation transport.NativeAllocation) error {
	c.alloc = allocation
	return nil
}

func TestSmallerRenderReusesWorkerReservation(t *testing.T) {
	host := staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "system", DeviceID: "ram", TotalBytes: 8 << 30, FreeBytes: 6 << 30}}
	recorder := &recordedMediaClaim{}
	worker := claimedService{Service: NewService(WithHostMemorySource(host)), claimant: recorder}
	cfg := transport.Config{Operation: "render-media", Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}}
	info := transport.ModelInfo{WeightsBytes: 1 << 30, RequiredBytes: 2 << 30, HostStagingBytes: 128 << 20}
	require.NoError(t, worker.claim(t.Context(), cfg, &info, info.WeightsBytes))
	peak := recorder.alloc.Resources.Pools[0].Bytes
	require.Equal(t, info.RequiredBytes+info.HostStagingBytes+capacity.NativeHostOverheadBytes, peak)
	info.RequiredBytes, info.HostStagingBytes = info.WeightsBytes, 1<<20
	require.NoError(t, worker.claim(t.Context(), cfg, &info, info.WeightsBytes))
	require.Equal(t, peak, recorder.alloc.Resources.Pools[0].Bytes)
}

func TestMediaRenderBudgetUsesDeviceAndHostMemory(t *testing.T) {
	s := transport.MediaSettings{Prompt: "lake", Width: 512, Height: 512, Frames: 49, FPS: 12, Steps: 24}
	device := capacity.DeviceSnapshot{Kind: "gpu", DeviceID: "gpu0", TotalBytes: 8 << 30, FreeBytes: 4 << 30}
	host := capacity.DeviceSnapshot{Kind: "system", TotalBytes: 16 << 30, FreeBytes: 6 << 30}
	info, err := mediaCapacity(s, 2<<30, device, host, capacity.Policy{})
	require.NoError(t, err)
	require.Greater(t, info.RequiredBytes, info.WeightsBytes)
	require.Positive(t, info.HostStagingBytes)

	_, err = mediaCapacity(s, 2<<30, device, host, capacity.Policy{MaxResidentBytes: 2400 << 20})
	require.ErrorIs(t, err, admission.ErrCapacity)
	small := s
	small.Frames = 9
	_, err = mediaCapacity(small, 2<<30, device, host, capacity.Policy{MaxResidentBytes: 2400 << 20})
	require.NoError(t, err)

	_, err = mediaCapacity(s, 2<<30, device, host, capacity.Policy{MinFreeBytes: 2 << 30})
	require.ErrorIs(t, err, admission.ErrCapacity)
	host.FreeBytes = 1 << 30
	device.Kind = "igpu"
	_, err = mediaCapacity(s, 2<<30, device, host, capacity.Policy{})
	require.ErrorIs(t, err, admission.ErrCapacity)
	device.Kind = "gpu"
	host.FreeBytes = 64 << 20
	_, err = mediaCapacity(s, 2<<30, device, host, capacity.Policy{})
	require.ErrorIs(t, err, admission.ErrCapacity)
}
