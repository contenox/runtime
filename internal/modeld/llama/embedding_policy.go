package llama

import (
	"fmt"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"os"
)

type embeddingPlan struct {
	path      string
	requested transport.Config
	resolved  transport.Config
	info      transport.ModelInfo
}

func (s *Service) resolveEmbedding(path string, cfg transport.Config) (transport.Config, transport.ModelInfo, error) {
	params, err := s.inspectModel(path)
	if err != nil {
		return transport.Config{}, transport.ModelInfo{}, err
	}
	device, err := capacity.Snapshot(s.memorySource(cfg))
	if err != nil {
		return transport.Config{}, transport.ModelInfo{}, fmt.Errorf("embedding memory probe: %w", err)
	}
	policy, err := s.resolvePolicy(device)
	if err != nil {
		return transport.Config{}, transport.ModelInfo{}, err
	}
	file, err := os.Stat(path)
	if err != nil {
		return transport.Config{}, transport.ModelInfo{}, fmt.Errorf("embedding model size: %w", err)
	}
	info := transport.ModelInfo{WeightsBytes: file.Size(), RequiredBytes: file.Size(), DeviceID: device.DeviceID, DeviceKind: device.Kind, DeviceTotalBytes: device.TotalBytes, FreeBytes: device.FreeBytes, MinFreeBytes: policy.MinFreeBytes}
	info.SupportsEmbedding = EmbedAvailable() && (params.PoolingType > 0 || cfg.EmbeddingPooling != "")
	info.ModelMaxContext = params.ContextLength
	info.EffectiveContext = cfg.NumCtx
	if info.EffectiveContext <= 0 {
		info.EffectiveContext = 4096
	}
	info.EffectiveContext = min(info.EffectiveContext, 32768)
	if !isAcceleratorSnapshot(device) {
		cfg.NumGpuLayers = 0
		cfg.Target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
		return cfg, info, nil
	}
	setting := policy.ComputeReserve
	if setting == "" {
		setting = os.Getenv("CONTENOX_LLAMA_GPU_COMPUTE_RESERVE")
	}
	compute := int64(0)
	if setting != "" && setting != "auto" {
		compute, err = capacity.ResolveComputeReserve(setting, 0, device)
		if err != nil {
			return transport.Config{}, transport.ModelInfo{}, err
		}
	}
	usable := max(device.FreeBytes-policy.MinFreeBytes, 0)
	if policy.MaxResidentBytes > 0 {
		usable = min(usable, policy.MaxResidentBytes)
	}
	usable = max(int64(float64(usable)*(1-policy.HeadroomFrac))-compute, 0)
	ceiling := params.BlockCount + 1
	if cfg.NumGpuLayers > 0 {
		ceiling = min(ceiling, cfg.NumGpuLayers)
	}
	cfg.NumGpuLayers = embeddingLayersForBudget(file.Size(), params.BlockCount, ceiling, usable)
	if cfg.Target.Normalize().Mode == transport.PlacementAccelerator && cfg.NumGpuLayers == 0 {
		return transport.Config{}, transport.ModelInfo{}, fmt.Errorf("%w: the embedding model does not fit the requested accelerator; "+remedy.EmbeddingRole, transport.ErrContextOverflow)
	}
	if len(cfg.TensorSplit) == 0 {
		if cfg.NumGpuLayers == 0 {
			cfg.Target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
		} else if device.DeviceID != "" {
			cfg.Target = transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: device.DeviceID}
		}
	}
	info.WeightsBytes = estimateLlamaGPUWeights(file.Size(), params.BlockCount, cfg.NumGpuLayers)
	info.OverheadBytes = compute
	info.RequiredBytes = info.WeightsBytes + compute
	if cfg.NumGpuLayers == 0 {
		info.WeightsBytes, info.RequiredBytes = file.Size(), file.Size()
	}
	return cfg, info, nil
}

func embeddingLayersForBudget(weights int64, blocks, ceiling int, budget int64) int {
	for layers := ceiling; layers > 0; layers-- {
		if estimateLlamaGPUWeights(weights, blocks, layers) <= budget {
			return layers
		}
	}
	return 0
}
