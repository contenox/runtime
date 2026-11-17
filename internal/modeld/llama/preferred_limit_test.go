package llama

import (
	"errors"
	"os"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
)

func TestUnit_AutoGPUFittingUsesPreferredTargetBeforePhysicalMemory(t *testing.T) {
	params := ggufParams{ContextLength: 1000, BlockCount: 4}
	device := capacity.DeviceSnapshot{Kind: "gpu", FreeBytes: 1000}
	policy := capacity.Policy{MaxResidentBytes: 400, MaxResidentPreferred: true, MinHotContextTokens: 100, HeadroomExplicit: true}
	layers := resolveGPULayersForBudget(0, transport.Config{NumGpuLayers: allGpuLayers}, params, capacity.LayerKVProfile{}, 500, 1, 0, device, policy)
	if layers != 3 {
		t.Fatalf("auto layers = %d, want 3 fitting the preferred target before physical expansion", layers)
	}
	policy.MaxResidentBytes = 50
	layers = resolveGPULayersForBudget(0, transport.Config{NumGpuLayers: allGpuLayers}, params, capacity.LayerKVProfile{}, 500, 1, 0, device, policy)
	if layers != 5 {
		t.Fatalf("auto layers = %d, want full physical fit after all target placements fail", layers)
	}
}

func TestUnit_ServicePreferredResidentTargetReportsPhysicalFit(t *testing.T) {
	params := ggufParams{ContextLength: 50, BlockCount: 4, HeadCountKV: 1, HeadCount: 1, KeyLength: 1}
	path := writeTestModelProfile(t, params)
	policy, err := capacity.DefaultPolicy().WithMemorySettings("1%", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	policy, err = policy.WithHeadroom("0%")
	if err != nil {
		t.Fatal(err)
	}
	policy.ComputeReserve = "0%"
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 2048, FreeBytes: 1024}}), WithHostMemorySource(staticMemory(0)))
	req := transport.OpenSessionRequest{Type: "llama", Path: path}
	info, err := svc.Describe(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if info.ResolvedGpuLayers != 5 || info.EffectiveContext != 50 || info.UserLimitBytes != 10 || info.UsableBytes != 1024 || info.Reason != "resident_target_exceeded" {
		t.Fatalf("preferred model info = %+v, want physical full-offload fit and original target", info)
	}
	cfg, err := svc.resolveConfig(req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NumGpuLayers != 5 || cfg.NumCtx != 50 {
		t.Fatalf("resolved config = %+v, want physical fit", cfg)
	}
}

func TestUnit_ServiceAutoGenerationRejectsPhysicallyImpossibleLayerPlacement(t *testing.T) {
	params := ggufParams{ContextLength: 50, BlockCount: 4, HeadCountKV: 1, HeadCount: 1, KeyLength: 1}
	path := writeTestModelProfile(t, params)
	if err := os.Truncate(path, 10000); err != nil {
		t.Fatal(err)
	}
	policy, err := capacity.DefaultPolicy().WithMemorySettings("1%", "0%", "0%")
	if err != nil {
		t.Fatal(err)
	}
	policy, err = policy.WithHeadroom("0%")
	if err != nil {
		t.Fatal(err)
	}
	policy.ComputeReserve = "0%"
	svc := NewService(WithCapacityPolicy(policy), WithMemorySource(staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 2048, FreeBytes: 1000}}), WithHostMemorySource(staticMemory(0)))
	_, err = svc.resolveConfig(transport.OpenSessionRequest{Type: "llama", Path: path})
	if !errors.Is(err, transport.ErrContextOverflow) {
		t.Fatalf("physically impossible auto placement = %v, want context overflow", err)
	}
}

func TestUnit_ServiceResidentLimitsPreserveHardAndPhysicalFailures(t *testing.T) {
	params := ggufParams{ContextLength: 100, BlockCount: 4, HeadCountKV: 1, HeadCount: 1, KeyLength: 1}
	path := writeTestModelProfile(t, params)
	for _, tc := range []struct {
		name     string
		resident string
		free     int64
	}{
		{name: "hard byte cap", resident: "10B", free: 1024},
		{name: "physical shortfall", resident: "1%", free: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := capacity.DefaultPolicy().WithMemorySettings(tc.resident, "0%", "0%")
			if err != nil {
				t.Fatal(err)
			}
			policy, err = policy.WithHeadroom("0%")
			if err != nil {
				t.Fatal(err)
			}
			policy.ComputeReserve = "0%"
			svc := NewService(WithCapacityPolicy(policy), WithMemorySource(staticSnapshot{snap: capacity.DeviceSnapshot{Kind: "gpu", TotalBytes: 2048, FreeBytes: tc.free}}), WithHostMemorySource(staticMemory(0)))
			_, err = svc.resolveConfig(transport.OpenSessionRequest{Type: "llama", Path: path, Config: transport.Config{NumGpuLayers: 5, NumCtx: 100}})
			if !errors.Is(err, transport.ErrContextOverflow) {
				t.Fatalf("resolveConfig = %v, want context overflow for %s", err, tc.name)
			}
		})
	}
}
