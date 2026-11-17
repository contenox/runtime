package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/contenox/runtime/internal/modeld/admission"
	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
)

func (t *Transport) workerService(ctx context.Context) (*modelworkers.Service, func() error, error) {
	if t.deps.DB == nil {
		return nil, nil, errors.New("settings database unavailable")
	}
	if t.deps.Engine != nil && t.deps.Engine.Workers != nil {
		service, err := modelworkers.New(t.deps.DB, t.deps.Engine.Workers)
		return service, func() error { return nil }, err
	}
	workers, err := modelworkers.NewConfigured(ctx, t.deps.DB)
	if err != nil {
		return nil, nil, err
	}
	service, err := modelworkers.New(t.deps.DB, workers)
	return service, workers.Close, err
}

// workerHost decodes a declared host selection into the engine's own request.
func workerHost(host *protocolv1.WorkerHost) modelworkers.InventoryRequest {
	out := modelworkers.InventoryRequest{Target: host.GetTarget(), ConnectionID: host.GetConnectionId()}
	if out.Target == "" {
		out.Target = "local"
	}
	return out
}

// workerExecutionTargetWire maps one placement selection onto its declared
// message.
func workerExecutionTargetWire(target transport.ExecutionTarget) *protocolv1.WorkerExecutionTarget {
	return &protocolv1.WorkerExecutionTarget{Mode: string(target.Mode), DeviceId: target.DeviceID}
}

// workerPoolAllocationWire maps one pool claim onto its declared message.
func workerPoolAllocationWire(pool admission.PoolAllocation) *protocolv1.WorkerPoolAllocation {
	return &protocolv1.WorkerPoolAllocation{
		PoolId: pool.PoolID, Bytes: pool.Bytes,
		LimitBytes: pool.LimitBytes, AvailableBytes: pool.AvailableBytes, TotalBytes: pool.TotalBytes,
	}
}

// workerPoolsWire maps pool claims onto their declared messages, always as a
// list so an unclaimed allocation renders an empty array.
func workerPoolsWire(pools []admission.PoolAllocation) []*protocolv1.WorkerPoolAllocation {
	out := make([]*protocolv1.WorkerPoolAllocation, 0, len(pools))
	for _, pool := range pools {
		out = append(out, workerPoolAllocationWire(pool))
	}
	return out
}

// workerProtectionWire maps one completion reservation onto its declared
// message.
func workerProtectionWire(protection admission.Protection) *protocolv1.WorkerProtection {
	return &protocolv1.WorkerProtection{
		ClientId: protection.ClientID, Identity: protection.Identity,
		Pools: workerPoolsWire(protection.Pools),
	}
}

// workerProtectionsWire maps completion reservations onto their declared
// messages.
func workerProtectionsWire(protections []admission.Protection) []*protocolv1.WorkerProtection {
	out := make([]*protocolv1.WorkerProtection, 0, len(protections))
	for _, protection := range protections {
		out = append(out, workerProtectionWire(protection))
	}
	return out
}

// workerAllocationWire maps one resident claim onto its declared message.
func workerAllocationWire(allocation admission.Allocation) *protocolv1.WorkerAllocation {
	return &protocolv1.WorkerAllocation{
		CanYield: allocation.CanYield, WorkerId: allocation.WorkerID,
		OwnerEpoch: allocation.OwnerEpoch, Identity: allocation.Identity,
		Pools:   workerPoolsWire(allocation.Pools),
		Threads: int32(allocation.Threads), ThreadLimit: int32(allocation.ThreadLimit),
	}
}

// workerResourcesWire maps the engine's resource projection onto its declared
// message.
func workerResourcesWire(resources admission.Resources) *protocolv1.WorkerResources {
	allocations := make([]*protocolv1.WorkerAllocation, 0, len(resources.Allocations))
	for _, allocation := range resources.Allocations {
		allocations = append(allocations, workerAllocationWire(allocation))
	}
	return &protocolv1.WorkerResources{
		Allocations: allocations, Protections: workerProtectionsWire(resources.Protections),
		Ceilings: resources.Ceilings,
	}
}

// workerDeviceWire maps one device onto its declared message.
func workerDeviceWire(device transport.DeviceInfo) *protocolv1.WorkerDevice {
	return &protocolv1.WorkerDevice{
		State: device.State, Error: device.Error, IdentityState: device.IdentityState,
		PoolId: device.PoolID, Operations: device.Operations, Id: device.ID,
		SharedMemory: device.SharedMemory, Index: int32(device.Index),
		Name: device.Name, Description: device.Description, Type: device.Type,
		MemoryFree: device.MemoryFree, MemoryTotal: device.MemoryTotal,
		MemoryFreeKnown: device.MemoryFreeKnown, MemoryTotalKnown: device.MemoryTotalKnown,
	}
}

// workerDevicesWire maps devices onto their declared messages.
func workerDevicesWire(devices []transport.DeviceInfo) []*protocolv1.WorkerDevice {
	out := make([]*protocolv1.WorkerDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, workerDeviceWire(device))
	}
	return out
}

// workerLaunchConfigWire maps one managed launch's configuration onto its
// declared message.
func workerLaunchConfigWire(config transport.Config) *protocolv1.WorkerLaunchConfig {
	return &protocolv1.WorkerLaunchConfig{
		Operation: config.Operation, AdmissionPermit: workerPermitWire(config.AdmissionPermit),
		Target: workerExecutionTargetWire(config.Target), Execution: workerExecutionWire(config.Execution),
		SessionNamespace: config.SessionNamespace, EmbeddingPooling: config.EmbeddingPooling,
		NumCtx: int32(config.NumCtx), HotContextTokens: int32(config.HotContextTokens),
		PlannerEffectiveContext: int32(config.PlannerEffectiveContext), NumBatch: int32(config.NumBatch),
		NumThreads: int32(config.NumThreads), NumGpuLayers: int32(config.NumGpuLayers),
		TensorSplit: config.TensorSplit, FlashAttn: config.FlashAttn, KvCacheType: config.KVCacheType,
		PromptFormat: config.PromptFormat, PromptTemplateDigest: config.PromptTemplateDigest,
		DisableBos: config.DisableBOS, ReasoningFormat: config.ReasoningFormat,
	}
}

// workerPermitWire maps one owner permit onto its declared message.
func workerPermitWire(permit admission.Permit) *protocolv1.WorkerPermit {
	return &protocolv1.WorkerPermit{Id: permit.ID, WorkerId: permit.WorkerID, OwnerEpoch: permit.OwnerEpoch}
}

// workerExecutionWire maps a reviewed allocation onto its declared message; an
// absent allocation stays absent.
func workerExecutionWire(execution *transport.Execution) *protocolv1.WorkerExecution {
	if execution == nil {
		return nil
	}
	return &protocolv1.WorkerExecution{
		Context: int32(execution.Context), Threads: int32(execution.Threads), Batch: int32(execution.Batch),
		KvCacheType: execution.KVCacheType, ClientId: execution.ClientID,
		Completion: &protocolv1.WorkerCompletionProtection{
			State: execution.Completion.State, Reservations: workerProtectionsWire(execution.Completion.Reservations),
		},
		Target: workerExecutionTargetWire(execution.Target), Role: execution.Role,
		Priority: execution.Priority, RequesterId: execution.RequesterID, Supersede: execution.Supersede,
		Memory: execution.Memory, Reserve: execution.Reserve, ColdMemory: execution.ColdMemory,
		IdleTtl: execution.IdleTTL, Headroom: execution.Headroom,
		ComputeReserve: execution.ComputeReserve, MinHotContext: execution.MinHotContext,
	}
}

// workerSpecWire maps one managed process's spec onto its declared message.
func workerSpecWire(spec transport.WorkerSpec) *protocolv1.WorkerSpec {
	return &protocolv1.WorkerSpec{
		ConfigurationDigest: spec.ConfigurationDigest, Digest: spec.Digest,
		Config: workerLaunchConfigWire(spec.Config), Role: spec.Role, Model: spec.Model,
		Backend: spec.Backend, Memory: spec.Memory, Reserve: spec.Reserve,
		ColdMemory: spec.ColdMemory, IdleTtl: spec.IdleTTL, Headroom: spec.Headroom,
		ComputeReserve: spec.ComputeReserve, MinHotContext: spec.MinHotContext,
	}
}

// workerStatusWire maps one managed process onto its declared message.
func workerStatusWire(worker transport.WorkerStatus) *protocolv1.WorkerStatus {
	return &protocolv1.WorkerStatus{
		References: int32(worker.References), Devices: worker.Devices, Spec: workerSpecWire(worker.Spec),
		Endpoint: worker.Endpoint, Instance: worker.Instance, State: worker.State, Error: worker.Error,
		QueuedForeground: int32(worker.QueuedForeground), QueuedBackground: int32(worker.QueuedBackground),
	}
}

// workerOptionsWire maps one role's reviewed launch options onto its declared
// message.
func workerOptionsWire(options modeldconn.WorkerOptions) *protocolv1.WorkerOptions {
	return &protocolv1.WorkerOptions{
		SchedulingMode: options.SchedulingMode, Context: int32(options.Context),
		Threads: int32(options.Threads), Batch: int32(options.Batch), KvCacheType: options.KVCacheType,
		Residency: options.Residency, Target: &protocolv1.WorkerExecutionTarget{
			Mode: string(options.Target.Mode), DeviceId: options.Target.DeviceID,
		},
		Backend: options.Backend, Memory: options.Memory, Reserve: options.Reserve,
		ColdMemory: options.ColdMemory, IdleTtl: options.IdleTTL, Headroom: options.Headroom,
		ComputeReserve: options.ComputeReserve, MinHotContext: options.MinHotContext,
	}
}

// workerSnapshotWire maps the engine's status snapshot onto its declared
// message; Options and OptionErrors are always maps so a snapshot with no
// reviewed role renders empty objects rather than absent members.
func workerSnapshotWire(snapshot modelworkers.Snapshot) *protocolv1.WorkerSnapshot {
	options := make(map[string]*protocolv1.WorkerOptions, len(snapshot.Options))
	for role, value := range snapshot.Options {
		options[role] = workerOptionsWire(value)
	}
	optionErrors := make(map[string]string, len(snapshot.OptionErrors))
	for role, message := range snapshot.OptionErrors {
		optionErrors[role] = message
	}
	workers := make([]*protocolv1.WorkerStatus, 0, len(snapshot.Workers))
	for _, worker := range snapshot.Workers {
		workers = append(workers, workerStatusWire(worker))
	}
	return &protocolv1.WorkerSnapshot{
		Resources: workerResourcesWire(snapshot.Resources), Contract: serviceContractWire(snapshot.Contract),
		Admission: &protocolv1.WorkerAdmissionPolicy{
			QueueCapacity: int32(snapshot.Admission.QueueCapacity), GlobalLimit: int32(snapshot.Admission.GlobalLimit),
			ForegroundBurst: int32(snapshot.Admission.ForegroundBurst), MaxQueueWait: snapshot.Admission.MaxQueueWait,
		},
		Options: options, OptionErrors: optionErrors, Workers: workers,
		OwnerState: snapshot.OwnerState, OwnerError: snapshot.OwnerError,
		AdmissionError: snapshot.AdmissionError,
	}
}

// workerInventoryWire maps a host's native inventory onto its declared message.
func workerInventoryWire(inventory transport.Inventory) *protocolv1.WorkerInventory {
	backends := make([]*protocolv1.WorkerBackend, 0, len(inventory.Backends))
	for _, backend := range inventory.Backends {
		backends = append(backends, &protocolv1.WorkerBackend{
			Scheduling: &protocolv1.WorkerScheduling{
				SingleActiveRequest: backend.Scheduling.SingleActiveRequest,
				CooperativeYield:    backend.Scheduling.CooperativeYield,
				ThreadControl:       backend.Scheduling.ThreadControl,
				YieldOperations:     backend.Scheduling.YieldOperations,
				Limitations:         backend.Scheduling.Limitations,
			},
			Backend: backend.Backend, State: backend.State, Error: backend.Error,
			RuntimeDigest: backend.RuntimeDigest, Operations: backend.Operations,
			Devices: workerDevicesWire(backend.Devices),
		})
	}
	pools := make([]*protocolv1.WorkerMemoryPool, 0, len(inventory.Pools))
	for _, pool := range inventory.Pools {
		pools = append(pools, &protocolv1.WorkerMemoryPool{
			Id: pool.ID, Kind: pool.Kind, TotalBytes: pool.TotalBytes,
			AvailableBytes: pool.AvailableBytes, State: pool.State, Error: pool.Error,
		})
	}
	return &protocolv1.WorkerInventory{HostThreads: int32(inventory.HostThreads), Backends: backends, Pools: pools}
}

// workerModelInfoWire maps a capacity reading onto its declared message; an
// absent reading stays absent rather than becoming a zero-valued model.
func workerModelInfoWire(info *transport.ModelInfo) *protocolv1.WorkerModelInfo {
	if info == nil {
		return nil
	}
	return &protocolv1.WorkerModelInfo{
		Allocation:        workerNativeAllocationWire(info.Allocation),
		SupportsEmbedding: info.SupportsEmbedding, ModelMaxContext: int32(info.ModelMaxContext),
		EffectiveContext: int32(info.EffectiveContext), MemoryContextTokens: int32(info.MemoryContextTokens),
		HotContextTokens: int32(info.HotContextTokens), PlannerEffectiveContext: int32(info.PlannerEffectiveContext),
		KvBytesPerToken: info.KVBytesPerToken, FreeBytes: info.FreeBytes, WeightsBytes: info.WeightsBytes,
		OverheadBytes: info.OverheadBytes, ReservedBytes: info.ReservedBytes,
		UserLimitBytes: info.UserLimitBytes, MinFreeBytes: info.MinFreeBytes,
		HostColdBudgetBytes: info.HostColdBudgetBytes, UsableBytes: info.UsableBytes,
		RequiredBytes: info.RequiredBytes, Clamped: info.Clamped, Reason: info.Reason,
		DeviceKind: info.DeviceKind, DeviceId: info.DeviceID,
		DeviceTotalBytes: info.DeviceTotalBytes, SharedWithDisplay: info.SharedWithDisplay,
	}
}

// workerNativeAllocationWire maps a native allocation onto its declared message.
func workerNativeAllocationWire(allocation *transport.NativeAllocation) *protocolv1.WorkerNativeAllocation {
	if allocation == nil {
		return nil
	}
	return &protocolv1.WorkerNativeAllocation{
		Devices: workerDevicesWire(allocation.Devices), Config: workerLaunchConfigWire(allocation.Config),
		Resources: workerAllocationWire(allocation.Resources),
	}
}

// workerInspectionWire maps one worker's inspection onto its declared message.
func workerInspectionWire(inspection transport.WorkerInspection) *protocolv1.WorkerInspection {
	status := inspection.Status
	workers := make([]*protocolv1.WorkerStatus, 0, len(status.Workers))
	for _, worker := range status.Workers {
		workers = append(workers, workerStatusWire(worker))
	}
	return &protocolv1.WorkerInspection{
		Status: &protocolv1.WorkerDaemonStatus{
			Workers: workers, OwnerInstanceId: status.OwnerInstanceID, Backend: status.Backend,
			State: string(status.State), Active: workerActiveModelWire(status.Active),
			BusyOperation: status.BusyOperation, LastError: status.LastError,
		},
		Capacity:      workerModelInfoWire(inspection.Capacity),
		CapacityError: inspection.CapacityError,
	}
}

// workerActiveModelWire maps a slot's active model onto its declared message.
func workerActiveModelWire(active *transport.ActiveModel) *protocolv1.WorkerActiveModel {
	if active == nil {
		return nil
	}
	adapters := make([]*protocolv1.WorkerAdapter, 0, len(active.Adapters))
	for _, adapter := range active.Adapters {
		adapters = append(adapters, &protocolv1.WorkerAdapter{
			Name: adapter.Name, Path: adapter.Path, Digest: adapter.Digest, Scale: adapter.Scale,
		})
	}
	return &protocolv1.WorkerActiveModel{
		ModelName: active.ModelName, Type: active.Type, Digest: active.Digest, Path: active.Path,
		Config: workerLaunchConfigWire(active.Config), Adapters: adapters, Generation: active.Generation,
	}
}

// executeAtHost runs one engine operation against the selected host. The
// service addresses a connection by its own request, so the surface resolves
// the host selection here rather than in the engine's own vocabulary.
func executeAtHost(ctx context.Context, service *modelworkers.Service, host modelworkers.InventoryRequest, request modelworkers.Request) (*modelworkers.Snapshot, error) {
	if host.Target == "" || host.Target == "local" {
		return service.Execute(ctx, request)
	}
	return nil, fmt.Errorf("workers: %s target is not addressable from this surface", host.Target)
}

// workerInspectAt reads one worker on the selected host.
func workerInspectAt(ctx context.Context, service *modelworkers.Service, host modelworkers.InventoryRequest, instance string) (transport.WorkerInspection, error) {
	if host.Target == "" || host.Target == "local" {
		result, err := service.Inspect(ctx, instance)
		if err != nil {
			return transport.WorkerInspection{}, err
		}
		return *result, nil
	}
	return transport.WorkerInspection{}, fmt.Errorf("workers: %s target is not addressable from this surface", host.Target)
}

// workerOperation runs one engine operation against the selected host and maps
// what it answers onto that operation's declared message.
func (t *Transport) workerOperation(ctx context.Context, request modelworkers.Request, host modelworkers.InventoryRequest) (json.RawMessage, *libacp.Error) {
	service, closeClient, err := t.workerService(ctx)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	defer closeClient()
	result, err := executeAtHost(ctx, service, host, request)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return marshalProtocol(workerSnapshotWire(*result))
}

func (t *Transport) handleWorkerStatus(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/status", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	return t.workerOperation(ctx, modelworkers.Request{Action: "status"}, workerHost(p.GetHost()))
}

func (t *Transport) handleWorkerRestart(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/restart", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	return t.workerOperation(ctx, modelworkers.Request{Action: "restart"}, workerHost(p.GetHost()))
}

func (t *Transport) handleWorkerConfigure(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerConfigureRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/configure", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.GetRole() == "" {
		return nil, libacp.InvalidParams("workers/configure: role is required")
	}
	if t.deps.OpenInference == nil {
		return nil, libacp.InternalError("inference configuration is unavailable")
	}
	setup, _, closeSetup, err := t.deps.OpenInference(ctx)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	defer closeSetup()
	if _, err := setup.ConfigureWorker(ctx, p.GetRole(), workerOptionsEngine(p.GetOptions())); err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return t.workerOperation(ctx, modelworkers.Request{Action: "status"}, workerHost(p.GetHost()))
}

// workerOptionsEngine decodes one role's declared launch options into the
// engine's own type.
func workerOptionsEngine(options *protocolv1.WorkerOptions) modeldconn.WorkerOptions {
	if options == nil {
		return modeldconn.WorkerOptions{}
	}
	return modeldconn.WorkerOptions{
		SchedulingMode: options.GetSchedulingMode(), Context: int(options.GetContext()),
		Threads: int(options.GetThreads()), Batch: int(options.GetBatch()),
		KVCacheType: options.GetKvCacheType(), Residency: options.GetResidency(),
		Target: transport.ExecutionTarget{
			Mode:     transport.PlacementMode(options.GetTarget().GetMode()),
			DeviceID: options.GetTarget().GetDeviceId(),
		},
		Backend: options.GetBackend(), Memory: options.GetMemory(), Reserve: options.GetReserve(),
		ColdMemory: options.GetColdMemory(), IdleTTL: options.GetIdleTtl(), Headroom: options.GetHeadroom(),
		ComputeReserve: options.GetComputeReserve(), MinHotContext: options.GetMinHotContext(),
	}
}

func (t *Transport) handleWorkerAdmission(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerAdmissionRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/admission", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	policy := modelworkers.Policy{
		QueueCapacity: int(p.GetQueueCapacity()), GlobalLimit: int(p.GetGlobalLimit()),
		ForegroundBurst: int(p.GetForegroundBurst()), MaxQueueWait: p.GetMaxQueueWait(),
	}
	return t.workerOperation(ctx, modelworkers.Request{Action: "admission", Policy: policy}, workerHost(p.GetHost()))
}

func (t *Transport) handleWorkerInventory(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerHost{}
	if rpcErr := unmarshalProtocolRequest("workers/inventory", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	service, closeClient, err := t.workerService(ctx)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	defer closeClient()
	result, err := service.Inventory(ctx, workerHost(p))
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return marshalProtocol(workerInventoryWire(result))
}

func (t *Transport) handleWorkerInspect(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerStopRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/inspect", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.GetInstance() == "" {
		return nil, libacp.InvalidParams("workers/inspect: instance is required")
	}
	service, closeClient, err := t.workerService(ctx)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	defer closeClient()
	result, err := workerInspectAt(ctx, service, workerHost(p.GetHost()), p.GetInstance())
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return marshalProtocol(workerInspectionWire(result))
}

func (t *Transport) handleWorkerStop(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p := &protocolv1.WorkerStopRequest{}
	if rpcErr := unmarshalProtocolRequest("workers/stop", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.GetInstance() == "" {
		return nil, libacp.InvalidParams("workers/stop: instance is required")
	}
	return t.workerOperation(ctx, modelworkers.Request{Action: "stop", Instance: p.GetInstance()}, workerHost(p.GetHost()))
}
