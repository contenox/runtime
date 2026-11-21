package openvino

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/diffusion"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

type claimedService struct {
	*Service
	claimant  transport.DeviceClaimant
	mediaMu   sync.Mutex
	mediaPeak map[string]int64
}

// WithDeviceClaims binds text, vision and embedding allocation to the shared worker owner.
func (s *Service) WithDeviceClaims(claimant transport.DeviceClaimant) (transport.Service, error) {
	if claimant == nil {
		return nil, errors.New("managed OpenVINO execution requires device claims")
	}
	return &claimedService{Service: s, claimant: claimant}, nil
}

func (s *claimedService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	return s.openSessions(ctx, req, s.claim)
}

func (s *claimedService) Embed(ctx context.Context, req transport.EmbedRequest) (transport.EmbedResult, error) {
	return s.embed(ctx, req, s.claim)
}

func (s *claimedService) Describe(ctx context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return transport.ModelInfo{}, err
	}
	if req.Config.NumThreads != 0 && req.Config.NumThreads != runtime.NumCPU() {
		return transport.ModelInfo{}, errors.New("the packaged OpenVINO backend does not expose a CPU thread limit; " + remedy.ReplanRole + ", or " + remedy.ChooseBackend)
	}
	genai, err := configForTarget(req.Path, req.Config.Target)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	genai.Device = effectiveDevice(genai.Device)
	var info transport.ModelInfo
	var cfg transport.Config
	weights := dirSize(req.Path)
	if req.Config.Operation == "embedding" || req.Config.Operation == "render-media" {
		snapshot, err := capacity.Snapshot(s.memorySource(genai.Device))
		if err != nil {
			return transport.ModelInfo{}, err
		}
		if req.Config.Operation == "render-media" {
			pipeline, err := diffusion.Pipeline(req.Path)
			if err != nil {
				return transport.ModelInfo{}, err
			}
			weights, err = mediaWeights(req.Path, pipeline)
			if err != nil {
				return transport.ModelInfo{}, err
			}
		}
		info = transport.ModelInfo{DeviceKind: snapshot.Kind, DeviceID: snapshot.DeviceID, WeightsBytes: weights, RequiredBytes: weights}
		info.SupportsEmbedding = req.Config.Operation == "embedding" && ovsession.EmbedAvailable()
		cfg = req.Config
	} else {
		info, err = s.describeConfigured(req, genai)
		if err != nil {
			return transport.ModelInfo{}, err
		}
		cfg, err = resolveConfigFromInfo(req.Config, info)
		if err != nil {
			return transport.ModelInfo{}, err
		}
	}
	cfg.Target = resolvedTarget(genai.Device, info.DeviceID)
	allocation, err := s.allocation(cfg, info, weights)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	info.Allocation = &allocation
	return info, nil
}

func (s *claimedService) claim(ctx context.Context, cfg transport.Config, info *transport.ModelInfo, weights int64) error {
	allocation, err := s.allocation(cfg, *info, weights)
	if err != nil {
		return err
	}
	if cfg.Operation == "render-media" {
		s.mediaMu.Lock()
		defer s.mediaMu.Unlock()
		for i, pool := range allocation.Resources.Pools {
			allocation.Resources.Pools[i].Bytes = max(pool.Bytes, s.mediaPeak[pool.PoolID])
		}
	}
	if err := s.claimant.ClaimDevices(ctx, allocation); err != nil {
		return err
	}
	if cfg.Operation == "render-media" {
		s.mediaPeak = make(map[string]int64, len(allocation.Resources.Pools))
		for _, pool := range allocation.Resources.Pools {
			s.mediaPeak[pool.PoolID] = pool.Bytes
		}
	}
	info.Allocation = &allocation
	return nil
}

func (s *claimedService) allocation(cfg transport.Config, info transport.ModelInfo, weights int64) (transport.NativeAllocation, error) {
	devices := []transport.DeviceInfo{}
	if cfg.Target.Mode != transport.PlacementCPU {
		if cfg.Target.Mode != transport.PlacementAccelerator || cfg.Target.DeviceID == "" {
			return transport.NativeAllocation{}, errors.New("native allocation has no resolved device identity; " + remedy.ReplanPlacement)
		}
		for _, device := range RuntimeInfo().Devices {
			if device.ID == cfg.Target.DeviceID {
				devices = append(devices, device)
			}
		}
		if len(devices) != 1 {
			return transport.NativeAllocation{}, errors.New("resolved native device is unavailable or ambiguous; " + remedy.ReplanPlacement)
		}
	}
	host, err := capacity.Snapshot(s.hostMemorySource())
	if err != nil {
		return transport.NativeAllocation{}, err
	}
	allocation, err := sessionkit.Allocation(cfg, info, weights, devices, host)
	if err != nil {
		return transport.NativeAllocation{}, err
	}
	return allocation, nil
}

func directAllocation(ctx context.Context, _ transport.Config, _ *transport.ModelInfo, _ int64) error {
	return ctx.Err()
}
