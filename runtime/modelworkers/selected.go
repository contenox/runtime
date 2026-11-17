package modelworkers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (s *Service) selectedEndpoint(ctx context.Context, target InventoryRequest) (string, error) {
	switch target.Target {
	case "local":
		if target.ConnectionID != "" {
			return "", errors.New("local execution controls cannot select a connection")
		}
		return modeldconn.LocalSentinel, nil
	case "connection":
		if target.ConnectionID == "" {
			return "", errors.New("select a native connection")
		}
		backend, err := backendservice.New(s.db).Get(ctx, target.ConnectionID)
		if err != nil {
			return "", err
		}
		if backend.Type != "modeld" && backend.Type != "local" {
			return "", errors.New("selected connection does not expose native workers")
		}
		if backend.BaseURL == "" {
			return "", errors.New("the selected modeld connection has no endpoint; " + remedy.ReconnectEndpoint)
		}
		return backend.BaseURL, nil
	default:
		return "", errors.New("worker controls require an explicit local or connection target")
	}
}

// ExecuteAt operates the selected host's owner without consulting another host's leases or processes.
func (s *Service) ExecuteAt(ctx context.Context, target InventoryRequest, request Request) (*Snapshot, error) {
	address, err := s.selectedEndpoint(ctx, target)
	if err != nil {
		return nil, err
	}
	if address == modeldconn.LocalSentinel {
		return s.Execute(ctx, request)
	}
	result, err := s.configuration(ctx, runtimetypes.New(s.db.WithoutTransaction()))
	if err != nil {
		return nil, err
	}
	client, err := modeldconn.Endpoint(ctx, "controls:"+target.ConnectionID, address)
	if err != nil {
		if request.Action != "status" {
			return nil, err
		}
		result.OwnerError = err.Error()
		return result, nil
	}
	switch request.Action {
	case "status":
	case "stop":
		if err := client.UnloadModel(ctx, transport.UnloadModelRequest{Fence: transport.Fence{OwnerInstanceID: client.InstanceID}, WorkerInstance: request.Instance}); err != nil {
			return nil, err
		}
	case "admission":
		wait, err := time.ParseDuration(request.Policy.MaxQueueWait)
		if err != nil || wait < 0 || request.Policy.QueueCapacity <= 0 || request.Policy.GlobalLimit < 0 || request.Policy.ForegroundBurst <= 0 {
			return nil, errors.New("invalid admission bounds or waiting duration")
		}
		policy := admission.Config{QueueCapacity: request.Policy.QueueCapacity, GlobalLimit: request.Policy.GlobalLimit, ForegroundBurst: request.Policy.ForegroundBurst, MaxQueueWait: wait}
		if err := client.ConfigureAdmission(ctx, policy); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown worker operation %q", request.Action)
	}
	status, err := client.Status(ctx)
	if err != nil {
		result.OwnerError = err.Error()
		return result, nil
	}
	result.Workers, result.OwnerState = status.Workers, "ready"
	scheduling, err := client.Admission(ctx)
	if err != nil {
		result.AdmissionError = err.Error()
		return result, nil
	}
	policy := scheduling.Policy
	result.Resources = scheduling.Resources
	result.Admission = Policy{QueueCapacity: policy.QueueCapacity, GlobalLimit: policy.GlobalLimit, ForegroundBurst: policy.ForegroundBurst, MaxQueueWait: policy.MaxQueueWait.String()}
	result.AdmissionError = ""
	return result, nil
}

// InspectAt reads an existing worker through its selected host's owner.
func (s *Service) InspectAt(ctx context.Context, target InventoryRequest, instance string) (*Inspection, error) {
	address, err := s.selectedEndpoint(ctx, target)
	if err != nil {
		return nil, err
	}
	if address == modeldconn.LocalSentinel {
		return s.Inspect(ctx, instance)
	}
	client, err := modeldconn.Endpoint(ctx, "controls:"+target.ConnectionID, address)
	if err != nil {
		return nil, err
	}
	result, err := client.InspectWorker(ctx, transport.WorkerInspectionRequest{Fence: transport.Fence{OwnerInstanceID: client.InstanceID}, WorkerInstance: instance})
	return &result, err
}
