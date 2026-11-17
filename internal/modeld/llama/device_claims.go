package llama

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

type claimedService struct {
	*Service
	claimant transport.DeviceClaimant
	planMu   sync.Mutex
	prepared *preparedSession
}

type preparedSession struct {
	request transport.OpenSessionRequest
	plan    sessionPlan
}

// WithDeviceClaims binds native allocation to a managed worker's owner before loading weights or projectors.
func (s *Service) WithDeviceClaims(claimant transport.DeviceClaimant) (transport.Service, error) {
	if claimant == nil {
		return nil, errors.New("managed llama execution requires device claims")
	}
	return &claimedService{Service: s, claimant: claimant}, nil
}

func (s *claimedService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	if req.Type != "" && req.Type != "llama" {
		return nil, fmt.Errorf("%w: requested %q, this daemon serves llama", transport.ErrBackendMismatch, req.Type)
	}
	plan, err := s.preparedPlan(req)
	if err != nil {
		return nil, err
	}
	return s.openPlannedSession(ctx, req, plan, s.claim)
}

func (s *claimedService) preparedPlan(req transport.OpenSessionRequest) (sessionPlan, error) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if s.prepared != nil && reflect.DeepEqual(preparedRequest(req), s.prepared.request) {
		plan := s.prepared.plan
		plan.config.SessionNamespace = req.Config.SessionNamespace
		plan.config.AdmissionPermit = req.Config.AdmissionPermit
		plan.config.Execution = req.Config.Execution
		plan.config.Operation = req.Config.Operation
		return plan, nil
	}
	plan, err := s.resolveSession(req)
	if err != nil {
		return sessionPlan{}, err
	}
	if plan.config.NumBatch <= 0 {
		plan.config.NumBatch = 512
	}
	if plan.config.ReasoningFormat == "" {
		plan.config.ReasoningFormat = plan.info.ChatTemplateReasoningFormat
	}
	allocation, err := s.allocation(plan.config, plan.info, fileSize(req.Path), true)
	if err != nil {
		return sessionPlan{}, err
	}
	plan.config = allocation.Config
	req.Config = plan.config
	s.prepared = &preparedSession{request: preparedRequest(req), plan: plan}
	return plan, nil
}

func preparedRequest(req transport.OpenSessionRequest) transport.OpenSessionRequest {
	req.Fence = transport.Fence{}
	req.ReclaimableBytes = 0
	req.Config.SessionNamespace = ""
	req.Config.AdmissionPermit = transport.Config{}.AdmissionPermit
	req.Config.Execution = nil
	if req.Config.Operation == "autocomplete" || req.Config.Operation == "next-edit" {
		req.Config.Operation = "chat"
	}
	return req
}

func (s *claimedService) Embed(ctx context.Context, req transport.EmbedRequest) (transport.EmbedResult, error) {
	return s.embed(ctx, req, s.claimEmbedding)
}

func (s *claimedService) Describe(ctx context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return transport.ModelInfo{}, err
	}
	var cfg transport.Config
	var info transport.ModelInfo
	if req.Config.Operation == "embedding" {
		var err error
		cfg, info, err = s.resolveEmbedding(req.Path, applyDaemonEnvOverrides(req.Config))
		if err != nil {
			return transport.ModelInfo{}, err
		}
	} else {
		plan, err := s.preparedPlan(req)
		if err != nil {
			return transport.ModelInfo{}, err
		}
		cfg, info = plan.config, plan.info
	}
	allocation, err := s.allocation(cfg, info, fileSize(req.Path), req.Config.Operation != "embedding")
	if err != nil {
		return transport.ModelInfo{}, err
	}
	info.Allocation = &allocation
	return info, nil
}

func (s *claimedService) claim(ctx context.Context, cfg transport.Config, info *transport.ModelInfo, weights int64) error {
	return s.claimAllocation(ctx, cfg, info, weights, true)
}

func (s *claimedService) claimEmbedding(ctx context.Context, cfg transport.Config, info *transport.ModelInfo, weights int64) error {
	return s.claimAllocation(ctx, cfg, info, weights, false)
}

func (s *claimedService) claimAllocation(ctx context.Context, cfg transport.Config, info *transport.ModelInfo, weights int64, canYield bool) error {
	allocation, err := s.allocation(cfg, *info, weights, canYield)
	if err != nil {
		return err
	}
	if err := s.claimant.ClaimDevices(ctx, allocation); err != nil {
		return err
	}
	info.Allocation = &allocation
	return nil
}

func (s *claimedService) allocation(cfg transport.Config, info transport.ModelInfo, weights int64, canYield bool) (transport.NativeAllocation, error) {
	devices, err := AllocationDevices(cfg, RuntimeInfo().Devices)
	if err != nil {
		return transport.NativeAllocation{}, err
	}
	host, err := capacity.Snapshot(s.hostMemorySource())
	if err != nil {
		return transport.NativeAllocation{}, err
	}
	allocation, err := sessionkit.Allocation(cfg, info, weights, devices, host)
	if err != nil {
		return transport.NativeAllocation{}, err
	}
	allocation.Resources.CanYield = canYield
	return allocation, nil
}

// AllocationDevices resolves every participating accelerator from the normalized native configuration.
func AllocationDevices(cfg transport.Config, inventory []transport.DeviceInfo) ([]transport.DeviceInfo, error) {
	devices := []transport.DeviceInfo{}
	if cfg.Target.Mode == transport.PlacementCPU || cfg.NumGpuLayers == 0 {
		return devices, nil
	}
	accelerators := []transport.DeviceInfo{}
	for _, device := range inventory {
		if device.Type == "gpu" || device.Type == "igpu" || device.Type == "accel" {
			accelerators = append(accelerators, device)
		}
	}
	if len(cfg.TensorSplit) > 0 {
		if cfg.Target.Mode == transport.PlacementAccelerator {
			return nil, errors.New("a physical device selection cannot also specify a multi-device tensor split")
		}
		for index, share := range cfg.TensorSplit {
			if share < 0 {
				return nil, errors.New("tensor split contains a negative allocation")
			}
			if share > 0 {
				if index >= len(accelerators) || accelerators[index].ID == "" {
					return nil, fmt.Errorf("tensor split device %d is unavailable; "+remedy.ReplanPlacement, index)
				}
				devices = append(devices, accelerators[index])
			}
		}
		if len(devices) == 0 {
			return nil, errors.New("tensor split requires at least one participating accelerator")
		}
		return devices, nil
	}
	for _, device := range accelerators {
		if device.ID == cfg.Target.DeviceID && device.ID != "" {
			devices = append(devices, device)
		}
	}
	if len(devices) != 1 {
		return nil, fmt.Errorf("resolved accelerator %q is unavailable or ambiguous; "+remedy.ReplanPlacement, cfg.Target.DeviceID)
	}
	return devices, nil
}

func directAllocation(ctx context.Context, _ transport.Config, _ *transport.ModelInfo, _ int64) error {
	return ctx.Err()
}
