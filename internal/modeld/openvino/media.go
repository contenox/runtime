package openvino

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/diffusion"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/internal/transport"
)

func (s *Service) RenderMedia(ctx context.Context, req transport.MediaRequest) (transport.MediaResult, error) {
	return s.renderMedia(ctx, req, directAllocation)
}

func (s *claimedService) RenderMedia(ctx context.Context, req transport.MediaRequest) (transport.MediaResult, error) {
	return s.renderMedia(ctx, req, s.claim)
}

func (s *Service) renderMedia(ctx context.Context, req transport.MediaRequest, claim func(context.Context, transport.Config, *transport.ModelInfo, int64) error) (transport.MediaResult, error) {
	if !ovsession.GenAIAvailable {
		return transport.MediaResult{}, transport.ErrUnsupportedFeature
	}
	if err := req.Settings.Validate(); err != nil {
		return transport.MediaResult{}, err
	}
	pipeline, err := diffusion.Pipeline(req.Path)
	if err != nil {
		return transport.MediaResult{}, err
	}
	if transport.MediaRoleForPipeline(pipeline) != req.Settings.Role() {
		return transport.MediaResult{}, fmt.Errorf("model %q with pipeline %q does not support %s", req.ModelName, pipeline, req.Settings.Role())
	}
	cfg, err := configForTarget(req.Path, req.Config.Target)
	if err != nil {
		return transport.MediaResult{}, err
	}
	weights, err := mediaWeights(req.Path, pipeline)
	if err != nil {
		return transport.MediaResult{}, err
	}
	var failures []error
	for _, device := range openSessionDevices(cfg.Device, devicePriority()) {
		if err := ctx.Err(); err != nil {
			return transport.MediaResult{}, err
		}
		snapshot, err := capacity.Snapshot(s.memorySource(device))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", device, err))
			continue
		}
		resolved := req.Config
		resolved.Target = resolvedTarget(device, snapshot.DeviceID)
		host, err := capacity.Snapshot(s.hostMemorySource())
		if err != nil {
			return transport.MediaResult{}, fmt.Errorf("read host memory for media render: %w", err)
		}
		info, err := mediaCapacity(req.Settings, weights, snapshot, host, s.policy)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", device, err))
			continue
		}
		if err := claim(ctx, resolved, &info, weights); err != nil {
			return transport.MediaResult{}, err
		}
		pixels, err := ovsession.RenderMedia(ctx, req.Path, device, os.Getenv(tokenizersPathEnv), req.Settings)
		if err != nil {
			return transport.MediaResult{}, err
		}
		data, mime, err := ovsession.EncodeMedia(ctx, pixels, req.Settings)
		if err != nil {
			return transport.MediaResult{}, err
		}
		return transport.MediaResult{Data: data, MIME: mime, Execution: transport.CopyExecutionReport(transport.SessionExecution{
			ModelName: req.ModelName, Digest: req.Digest, Backend: "openvino", DeviceSelector: device,
			Requested: req.Config, Resolved: resolved, Capacity: info,
		})}, nil
	}
	return transport.MediaResult{}, fmt.Errorf("no OpenVINO device available for media generation: %w", errors.Join(failures...))
}

func mediaWeights(dir, pipeline string) (int64, error) {
	if pipeline == "LTXPipeline" {
		staged, err := diffusion.NeedsLTXAdapter(dir)
		if err != nil {
			return 0, err
		}
		if staged {
			return max(dirSize(filepath.Join(dir, "text_encoder")), dirSize(filepath.Join(dir, "transformer")), dirSize(filepath.Join(dir, "vae_decoder"))), nil
		}
	}
	return dirSize(dir), nil
}

func mediaCapacity(settings transport.MediaSettings, weights int64, device, host capacity.DeviceSnapshot, policy capacity.Policy) (transport.ModelInfo, error) {
	if err := settings.Validate(); err != nil {
		return transport.ModelInfo{}, err
	}
	shared := !device.IsAccelerator() || device.Kind == "igpu"
	if shared {
		device.FreeBytes = min(device.FreeBytes, host.FreeBytes)
	}
	policy, err := capacity.ResolvePolicy(policy, device, host)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	pixels := int64(settings.Width * settings.Height * settings.InferenceFrames())
	const estimatedActivationBytesPerPixel = 64
	const hostBufferBytesPerPixel = 12
	hostBuffers := pixels * hostBufferBytesPerPixel
	planned := capacity.Resolve(capacity.Params{
		WeightsBytes: weights, OverheadBytes: pixels * estimatedActivationBytesPerPixel,
		FreeBytes: device.FreeBytes, MinFreeBytes: policy.MinFreeBytes, UserLimitBytes: policy.MaxResidentBytes,
		HeadroomFrac: policy.HeadroomFrac, HeadroomExplicit: policy.HeadroomExplicit,
	})
	hostRequired := capacity.NativeHostOverheadBytes + hostBuffers
	required := planned.RequiredBytes
	if shared {
		required += hostRequired
	}
	if required > planned.UsableBytes {
		return transport.ModelInfo{}, fmt.Errorf("%w: %dx%d with %d generated frames needs an estimated %d MiB on %s, %d MiB usable; reduce dimensions or frame count, or choose a smaller model", admission.ErrCapacity, settings.Width, settings.Height, settings.InferenceFrames(), (required+(1<<20)-1)>>20, device.DeviceID, planned.UsableBytes>>20)
	}
	if hostRequired > host.FreeBytes {
		return transport.ModelInfo{}, fmt.Errorf("%w: media buffers need %d bytes of host memory, %d available", admission.ErrCapacity, hostRequired, host.FreeBytes)
	}
	return transport.ModelInfo{DeviceKind: device.Kind, DeviceID: device.DeviceID, DeviceTotalBytes: device.TotalBytes,
		FreeBytes: planned.FreeBytes, UsableBytes: planned.UsableBytes, MinFreeBytes: planned.MinFreeBytes, UserLimitBytes: planned.UserLimitBytes,
		WeightsBytes: weights, OverheadBytes: planned.OverheadBytes, RequiredBytes: planned.RequiredBytes, HostStagingBytes: hostBuffers}, nil
}
