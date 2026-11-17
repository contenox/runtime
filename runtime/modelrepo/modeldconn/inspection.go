package modeldconn

import (
	"context"
	"errors"
	"time"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
)

// InspectOwnedWorker reads a child selected by its owner's current pool inventory.
func InspectOwnedWorker(ctx context.Context, worker WorkerStatus) (transport.WorkerInspection, error) {
	if worker.Endpoint == "" || worker.Instance == "" {
		return transport.WorkerInspection{}, errors.New("worker endpoint and instance are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := transportgrpc.DialLeader(worker.Endpoint, worker.Instance)
	if err != nil {
		return transport.WorkerInspection{}, err
	}
	defer client.Close()
	status, err := client.Status(ctx)
	if err != nil {
		return transport.WorkerInspection{}, err
	}
	result := transport.WorkerInspection{Status: status}
	if active := status.Active; active != nil {
		info, err := client.Describe(ctx, transport.OpenSessionRequest{Fence: transport.Fence{OwnerInstanceID: worker.Instance}, ModelName: active.ModelName, Type: active.Type, Digest: active.Digest, Path: active.Path, Config: active.Config, Adapters: active.Adapters})
		if err != nil {
			result.CapacityError = err.Error()
		} else {
			result.Capacity = &info
		}
	}
	return result, nil
}

// InspectWorker reads a retained worker through its shared owner or the owning pool.
func (m *Workers) InspectWorker(ctx context.Context, instance string) (transport.WorkerInspection, error) {
	if m.shared {
		client, err := m.sharedEndpoint(ctx)
		if err != nil {
			return transport.WorkerInspection{}, err
		}
		return client.InspectWorker(ctx, transport.WorkerInspectionRequest{Fence: transport.Fence{OwnerInstanceID: client.InstanceID}, WorkerInstance: instance})
	}
	for _, worker := range m.Snapshot() {
		if worker.Instance == instance && instance != "" {
			return InspectOwnedWorker(ctx, worker)
		}
	}
	return transport.WorkerInspection{}, errors.New("selected worker is no longer owned by this daemon")
}
