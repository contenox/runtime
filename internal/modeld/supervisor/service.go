// Package supervisor owns shared modeld workers and their client sessions.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

// Pool manages owner-controlled children and retains admission until native work ends.
type Pool interface {
	Acquire(context.Context, modeldconn.WorkerSpec) (modeldconn.ModeldTarget, func() error, error)
	Retire(context.Context, modeldconn.ModeldTarget) error
	StopIdle(context.Context, string) error
	Snapshot() []modeldconn.WorkerStatus
	Status(context.Context) ([]modeldconn.WorkerStatus, error)
	Scheduler() admission.Service
	Close() error
}

// Admission reads the scheduler owned by this daemon epoch.
func (s *Service) Admission(ctx context.Context) (admission.Snapshot, error) {
	return s.pool.Scheduler().Snapshot(ctx)
}

// ConfigureAdmission changes shared queue policy through the existing scheduler.
func (s *Service) ConfigureAdmission(ctx context.Context, policy admission.Config) error {
	scheduler, ok := s.pool.Scheduler().(interface {
		Configure(context.Context, admission.Config) error
	})
	if !ok {
		return errors.New("owner admission policy is not configurable")
	}
	return scheduler.Configure(ctx, policy)
}

// ConfigureCeilings replaces the device ceilings through the existing scheduler.
func (s *Service) ConfigureCeilings(ctx context.Context, ceilings map[string]float64) error {
	scheduler, ok := s.pool.Scheduler().(interface {
		ConfigureCeilings(context.Context, map[string]float64) error
	})
	if !ok {
		return errors.New("owner device ceilings are not configurable")
	}
	return scheduler.ConfigureCeilings(ctx, ceilings)
}

// Status reports the owner epoch and its workers without loading a model.
func (s *Service) Status(ctx context.Context) (transport.DaemonStatus, error) {
	workers, err := s.pool.Status(ctx)
	if err != nil {
		return transport.DaemonStatus{}, err
	}
	return transport.DaemonStatus{OwnerInstanceID: s.epoch, Backend: "shared", State: transport.SlotReady, Workers: workers}, nil
}

// InspectWorker resolves a child locally and returns its observations through the owner connection.
func (s *Service) InspectWorker(ctx context.Context, request transport.WorkerInspectionRequest) (transport.WorkerInspection, error) {
	if request.OwnerInstanceID != s.epoch {
		return transport.WorkerInspection{}, transport.ErrStaleFence
	}
	workers, err := s.pool.Status(ctx)
	if err != nil {
		return transport.WorkerInspection{}, err
	}
	for _, worker := range workers {
		if worker.Instance == request.WorkerInstance && request.WorkerInstance != "" {
			return modeldconn.InspectOwnedWorker(ctx, worker)
		}
	}
	return transport.WorkerInspection{}, errors.New("selected worker is no longer owned by this daemon; refresh its worker inventory")
}

// LoadModel requires session-scoped managed execution so another client's allocation cannot be replaced.
func (s *Service) LoadModel(context.Context, transport.LoadModelRequest) (transport.ActiveModel, error) {
	return transport.ActiveModel{}, errors.New("shared modeld loads models through role sessions; " + remedy.AssignRole)
}

// UnloadModel releases a selected idle worker while preserving all active client sessions.
func (s *Service) UnloadModel(ctx context.Context, req transport.UnloadModelRequest) error {
	if req.OwnerInstanceID != s.epoch {
		return transport.ErrStaleFence
	}
	if req.WorkerInstance == "" {
		return errors.New("select an idle worker before unloading; " + remedy.StopIdleWorker)
	}
	return s.pool.StopIdle(ctx, req.WorkerInstance)
}

// Service routes requests through the shared owner's worker pool.
type Service struct {
	shutdown     chan struct{}
	shutdownOnce sync.Once
	pool         Pool
	models       *modelstore.NodeAdmin
	dir          string
	epoch        string
	inventory    func(context.Context) (transport.Inventory, error)
}

// New constructs a shared owner with an explicit child pool and model store.
func New(pool Pool, modelsDir, epoch string, inventory func(context.Context) (transport.Inventory, error)) (*Service, error) {
	if pool == nil || modelsDir == "" || epoch == "" || inventory == nil {
		return nil, errors.New("shared owner requires workers, model store and owner epoch")
	}
	service := &Service{pool: pool, dir: modelsDir, epoch: epoch, inventory: inventory, shutdown: make(chan struct{})}
	models, err := modelstore.NewProtectedNodeAdmin(modelsDir, service.mutableModel)
	if err != nil {
		return nil, err
	}
	service.models = models
	return service, nil
}

// Inventory reports backend availability and devices without allocating a native model.
func (s *Service) Inventory(ctx context.Context) (transport.Inventory, error) {
	return s.inventory(ctx)
}

// ClaimDevices delegates allocation ownership to the same pool that controls worker termination.
func (s *Service) ClaimDevices(ctx context.Context, request transport.DeviceClaimRequest) (transport.DeviceClaimResult, error) {
	if request.OwnerInstance != s.epoch {
		return transport.DeviceClaimResult{}, transport.ErrStaleFence
	}
	pool, ok := s.pool.(interface {
		ClaimDevices(context.Context, transport.DeviceClaimRequest) (transport.DeviceClaimResult, error)
	})
	if !ok {
		return transport.DeviceClaimResult{}, transport.ErrUnsupportedFeature
	}
	return pool.ClaimDevices(ctx, request)
}

func (s *Service) acquire(ctx context.Context, req transport.OpenSessionRequest) (modeldconn.ModeldTarget, func() error, transport.OpenSessionRequest, error) {
	if req.OwnerInstanceID != s.epoch {
		return modeldconn.ModeldTarget{}, nil, req, transport.ErrStaleFence
	}
	execution := req.Config.Execution
	if execution == nil {
		return modeldconn.ModeldTarget{}, nil, req, errors.New("managed local execution requires explicit role and allocation settings; update the IDE")
	}
	if err := execution.Target.Validate(); err != nil {
		return modeldconn.ModeldTarget{}, nil, req, err
	}
	if err := (modeldconn.WorkerOptions{Context: execution.Context, Threads: execution.Threads, Batch: execution.Batch, KVCacheType: execution.KVCacheType}).Validate(); err != nil {
		return modeldconn.ModeldTarget{}, nil, req, err
	}
	req.Config = execution.ApplySettings(req.Config)
	if req.Config.Target.Mode != "" && req.Config.Target.Normalize() != execution.Target.Normalize() {
		return modeldconn.ModeldTarget{}, nil, req, errors.New("session placement differs from the reviewed worker placement; " + remedy.ReplanRole)
	}
	req.Config.Target = execution.Target.Normalize()
	if execution.Role != "inspection" && execution.Completion.State != "" {
		resources, ok := s.pool.Scheduler().(admission.ResourceService)
		if !ok {
			return modeldconn.ModeldTarget{}, nil, req, transport.ErrUnsupportedFeature
		}
		switch execution.Completion.State {
		case "protected":
			if len(execution.Completion.Reservations) == 0 {
				return modeldconn.ModeldTarget{}, nil, req, errors.New("protected completion has no verified allocation; " + remedy.VerifyAutocomplete)
			}
		case "unconfigured", "external", "on-demand", "retain":
			if len(execution.Completion.Reservations) != 0 {
				return modeldconn.ModeldTarget{}, nil, req, errors.New("completion residency policy conflicts with its reservation")
			}
		default:
			return modeldconn.ModeldTarget{}, nil, req, errors.New("unknown completion residency policy")
		}
		if execution.Completion.State != "retain" {
			if err := resources.ProtectClient(ctx, execution.ClientID, execution.Completion.Reservations); err != nil {
				return modeldconn.ModeldTarget{}, nil, req, err
			}
		}
	}
	retain, err := s.models.Retain(req.ModelName)
	if err != nil {
		return modeldconn.ModeldTarget{}, nil, req, err
	}
	defer retain()
	path, err := modelstore.Resolve(s.dir, req.ModelName, req.Type, req.Digest)
	if err != nil {
		return modeldconn.ModeldTarget{}, nil, req, err
	}
	if req.Digest == "" {
		req.Digest, err = modelstore.ArtifactDigest(path, req.Type)
		if err != nil {
			return modeldconn.ModeldTarget{}, nil, req, err
		}
	}
	req.Path, req.Config.Execution = path, nil
	req.Config.AdmissionPermit = admission.Permit{}
	if execution.Role == "embedding" {
		req.Config.Operation = "embedding"
	} else if execution.Role == transport.ImageRole || execution.Role == transport.VideoRole {
		if req.Config.Operation != "render-media" {
			return modeldconn.ModeldTarget{}, nil, req, errors.New("the media worker role requires a media render request")
		}
	} else if (execution.Role != "inspection" || req.Config.Operation == "") && (execution.Role != "autocomplete" || req.Config.Operation != "autocomplete") && (execution.Role != "next-edit" || req.Config.Operation != "next-edit") {
		req.Config.Operation = "chat"
	}
	qos := modeldconn.AdmissionOptions{Priority: admission.Priority(execution.Priority), RequesterID: execution.RequesterID, Supersede: execution.Supersede}
	ctx = modeldconn.WithAdmissionOptions(ctx, qos)
	spec := modeldconn.WorkerSpec{
		Role: execution.Role, Model: req.ModelName, Digest: req.Digest, Backend: req.Type, Config: req.Config,
		Memory: execution.Memory, Reserve: execution.Reserve, ColdMemory: execution.ColdMemory, IdleTTL: execution.IdleTTL,
		Headroom: execution.Headroom, ComputeReserve: execution.ComputeReserve, MinHotContext: execution.MinHotContext,
	}
	target, release, err := s.pool.Acquire(ctx, spec)
	if target.Allocation != nil {
		namespace := req.Config.SessionNamespace
		operation := req.Config.Operation
		req.Config = target.Allocation.Config
		req.Config.SessionNamespace = namespace
		req.Config.Operation = operation
	}
	req.Config.AdmissionPermit = target.Permit
	return target, release, req, err
}

// WaitYield requests a cooperative boundary only within this owner's worker generation.
func (s *Service) WaitYield(ctx context.Context, permit admission.Permit) error {
	scheduler, ok := s.pool.Scheduler().(admission.YieldService)
	if !ok {
		return transport.ErrUnsupportedFeature
	}
	return scheduler.WaitYield(ctx, permit)
}

// ResumeAtBoundary waits for compute admission while retaining the worker's session permit.
func (s *Service) ResumeAtBoundary(ctx context.Context, permit admission.Permit) error {
	scheduler, ok := s.pool.Scheduler().(admission.YieldService)
	if !ok {
		return transport.ErrUnsupportedFeature
	}
	return scheduler.ResumeAtBoundary(ctx, permit)
}

// OpenSession retains worker admission until the client's native session has closed.
func (s *Service) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	requested := reviewedConfig(req.Config)
	target, release, req, err := s.acquire(ctx, req)
	if err != nil {
		return nil, err
	}
	control := watchPermit(ctx, s.pool.Scheduler(), target.Permit)
	openCtx, finishOpen := control.operation(ctx)
	sess, err := modeldconn.OpenSessionTarget(openCtx, target, ref(req), req.Config)
	finishOpen()
	if err != nil {
		err = errors.Join(control.cause(), err)
		control.close()
		if stopErr := s.retire(target); stopErr != nil {
			return nil, errors.Join(err, stopErr)
		}
		return nil, errors.Join(err, release())
	}
	managed := &session{Session: sess, control: control, release: release, retire: func() error { return s.retire(target) }, requested: requested, owner: s.epoch, worker: target.Instance, allocation: target.Allocation}
	go func() {
		<-control.ctx.Done()
		_ = managed.Close()
	}()
	return managed, nil
}

// CloseClient releases unused completion protection without changing worker allocations or another client's sessions.
func (s *Service) CloseClient(ctx context.Context, clientID string) error {
	resources, ok := s.pool.Scheduler().(admission.ResourceService)
	if !ok {
		return transport.ErrUnsupportedFeature
	}
	return resources.ProtectClient(ctx, clientID, []admission.Protection{})
}

// Describe checks the requested model and allocation in its compatible worker.
func (s *Service) Describe(ctx context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	target, release, req, err := s.acquire(ctx, req)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	info, err := modeldconn.DescribeTarget(ctx, target, ref(req), req.Config)
	return info, errors.Join(err, release())
}

// Embed holds admission until native embedding has completed or its worker has stopped.
func (s *Service) Embed(ctx context.Context, request transport.EmbedRequest) (transport.EmbedResult, error) {
	target, release, req, err := s.acquire(ctx, transport.OpenSessionRequest{Fence: request.Fence, ModelName: request.ModelName, Type: request.Type, Digest: request.Digest, Config: request.Config})
	if err != nil {
		return transport.EmbedResult{}, err
	}
	result, err := modeldconn.EmbedTarget(ctx, target, ref(req), req.Config, request.Text)
	if result.Execution != nil {
		result.Execution.Requested = reviewedConfig(request.Config)
		result.Execution.OwnerEpoch, result.Execution.WorkerIdentity = s.epoch, target.Instance
		if target.Allocation != nil && result.Execution.Capacity.Allocation != nil {
			result.Execution.Capacity.Allocation.Resources.Identity = target.Allocation.Resources.Identity
		}
		result.Execution = transport.CopyExecutionReport(*result.Execution)
	}
	if err != nil {
		if stopErr := s.retire(target); stopErr != nil {
			return result, errors.Join(err, stopErr)
		}
	}
	return result, errors.Join(err, release())
}

func (s *Service) RenderMedia(ctx context.Context, request transport.MediaRequest) (transport.MediaResult, error) {
	if request.Config.Execution == nil || request.Config.Execution.Role != request.Settings.Role() {
		return transport.MediaResult{}, errors.New("media request does not match the selected inference role")
	}
	request.Config.Operation = "render-media"
	requested := reviewedConfig(request.Config)
	target, release, resolved, err := s.acquire(ctx, request.OpenSessionRequest)
	if err != nil {
		return transport.MediaResult{}, err
	}
	request.OpenSessionRequest = resolved
	request.Path = "" // The worker resolves against its own retained model store.
	result, err := modeldconn.RenderMediaTarget(ctx, target, request)
	if result.Execution != nil {
		result.Execution.Requested = requested
		result.Execution.OwnerEpoch, result.Execution.WorkerIdentity = s.epoch, target.Instance
		if target.Allocation != nil && result.Execution.Capacity.Allocation != nil {
			result.Execution.Capacity.Allocation.Resources.Identity = target.Allocation.Resources.Identity
		}
		result.Execution = transport.CopyExecutionReport(*result.Execution)
	}
	if err != nil {
		err = errors.Join(err, s.retire(target))
	}
	return result, errors.Join(err, release())
}

func reviewedConfig(config transport.Config) transport.Config {
	if config.Execution != nil {
		config.Target = config.Execution.Target.Normalize()
		config = config.Execution.ApplySettings(config)
	}
	return config
}

func ref(req transport.OpenSessionRequest) modeldconn.ModelRef {
	return modeldconn.ModelRef{Name: req.ModelName, Type: req.Type, Path: req.Path, Digest: req.Digest, Adapters: req.Adapters}
}

func (s *Service) retire(target modeldconn.ModeldTarget) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.pool.Retire(ctx, target)
}

// Close stops and joins all owned children before their allocations can be reclaimed.
func (s *Service) Close() error { return s.pool.Close() }

// ListModels lists installed artifacts without starting a worker or loading a model.
func (s *Service) ListModels(ctx context.Context) ([]transport.NodeModel, error) {
	return s.models.ListModels(ctx)
}

func (s *Service) mutableModel(name string) error {
	for _, worker := range s.pool.Snapshot() {
		if worker.Spec.Model == name {
			return fmt.Errorf("%w: model %q is retained by a shared worker; stop that worker before replacing or removing the artifact", transport.ErrModelBusy, name)
		}
	}
	return nil
}

// RemoveModel rejects removal while a shared worker retains the artifact.
func (s *Service) RemoveModel(ctx context.Context, name string) error {
	return s.models.RemoveModel(ctx, name)
}

// DiskStats reports the shared model store's filesystem capacity.
func (s *Service) DiskStats(ctx context.Context) (transport.NodeDiskStats, error) {
	return s.models.DiskStats(ctx)
}

// ReceiveModel installs a verified artifact when no worker retains its previous version.
func (s *Service) ReceiveModel(ctx context.Context, manifest transport.PushManifest, reader io.Reader) (transport.PushResult, error) {
	return s.models.ReceiveModel(ctx, manifest, reader)
}
