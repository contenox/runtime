// Package modelworkers owns IDE configuration and repair of shared local execution.
package modelworkers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
)

// Contract describes shared local execution configuration and repair.
var Contract = servicecontract.Contract{
	ID: "local-model-workers", Summary: "Manage local inference",
	Description: "Review local resource settings and the shared model owner. Inspect workers or stop an idle worker without interrupting another IDE's requests.",
	Operations: []servicecontract.Operation{
		{ID: "status", Summary: "Read settings and owner status"},
		{ID: "admission", Summary: "Configure shared scheduling"},
		{ID: "inspect", Summary: "Inspect worker placement and capacity"},
		{ID: "inventory", Summary: "Read native backends, devices and memory pools"},
		{ID: "stop", Summary: "Stop an idle worker"},
		{ID: "restart", Summary: "Restart the local owner after all native sessions close"},
	},
}

// Policy describes editable admission settings without exposing duration encodings.
type Policy struct {
	QueueCapacity   int    `json:"queue_capacity"`
	GlobalLimit     int    `json:"global_limit"`
	ForegroundBurst int    `json:"foreground_burst"`
	MaxQueueWait    string `json:"max_queue_wait"`
}

// Snapshot separates saved configuration from unavailable owner observations.
type Snapshot struct {
	Resources      admission.Resources                 `json:"resources"`
	Contract       servicecontract.Contract            `json:"contract"`
	Admission      Policy                              `json:"admission"`
	Options        map[string]modeldconn.WorkerOptions `json:"options"`
	OptionErrors   map[string]string                   `json:"option_errors"`
	Workers        []modeldconn.WorkerStatus           `json:"workers"`
	OwnerState     string                              `json:"owner_state"`
	OwnerError     string                              `json:"owner_error,omitempty"`
	AdmissionError string                              `json:"admission_error,omitempty"`
}

// Request selects one local execution operation.
type Request struct {
	Action   string
	Instance string
	Policy   Policy
}

// Inspection reports an existing worker's placement and resident allocation.
type Inspection = transport.WorkerInspection

// Service owns persisted local settings and shared-owner controls.
type Service struct {
	db      libdbexec.DBManager
	workers *modeldconn.Workers
}

// New constructs worker configuration with complete persistence and execution dependencies.
func New(db libdbexec.DBManager, workers *modeldconn.Workers) (*Service, error) {
	if db == nil || workers == nil {
		return nil, errors.New("worker configuration requires persistence and a local execution client")
	}
	return &Service{db: db, workers: workers}, nil
}

// Execute applies the selected operation and returns independently readable configuration and owner state.
func (s *Service) Execute(ctx context.Context, req Request) (*Snapshot, error) {
	store := runtimetypes.New(s.db.WithoutTransaction())
	switch req.Action {
	case "status":
	case "restart":
		if err := s.workers.RestartOwner(ctx); err != nil {
			return nil, err
		}
	case "admission":
		wait, err := time.ParseDuration(req.Policy.MaxQueueWait)
		if err != nil || wait < 0 || req.Policy.QueueCapacity <= 0 || req.Policy.GlobalLimit < 0 || req.Policy.ForegroundBurst <= 0 {
			return nil, errors.New("invalid admission bounds or waiting duration")
		}
		policy := admission.Config{QueueCapacity: req.Policy.QueueCapacity, GlobalLimit: req.Policy.GlobalLimit, ForegroundBurst: req.Policy.ForegroundBurst, MaxQueueWait: wait}
		configurable, ok := s.workers.Scheduler().(interface {
			Configure(context.Context, admission.Config) error
		})
		if !ok {
			return nil, errors.New("local owner does not expose scheduling configuration; update local inference")
		}
		if err := configurable.Configure(ctx, policy); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(policy)
		if err != nil {
			return nil, err
		}
		if err := clikv.SetString(ctx, store, "inference.modeld.admission", string(raw)); err != nil {
			return nil, fmt.Errorf("owner scheduling changed but its client preference could not be saved: %w", err)
		}
	case "stop":
		if err := s.workers.StopIdle(ctx, req.Instance); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown worker operation %q", req.Action)
	}
	return s.snapshot(ctx, store)
}

func (s *Service) configuration(ctx context.Context, store runtimetypes.Store) (*Snapshot, error) {
	result := &Snapshot{Contract: Contract, Options: map[string]modeldconn.WorkerOptions{}, OptionErrors: map[string]string{}, Workers: []modeldconn.WorkerStatus{}, OwnerState: "unavailable"}
	for _, role := range []string{"primary", "alternative", "autocomplete", "next-edit", "embedding", transport.ImageRole, transport.VideoRole} {
		options, err := ReadOptions(ctx, store, role)
		if err != nil {
			result.OptionErrors[role] = err.Error()
		}
		result.Options[role] = options
	}
	policy := admission.DefaultConfig()
	var raw string
	if err := store.GetKV(ctx, clikv.Prefix+"inference.modeld.admission", &raw); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
		return nil, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			result.AdmissionError = err.Error()
		}
	}
	result.Admission = Policy{QueueCapacity: policy.QueueCapacity, GlobalLimit: policy.GlobalLimit, ForegroundBurst: policy.ForegroundBurst, MaxQueueWait: policy.MaxQueueWait.String()}
	return result, nil
}

func (s *Service) snapshot(ctx context.Context, store runtimetypes.Store) (*Snapshot, error) {
	result, err := s.configuration(ctx, store)
	if err != nil {
		return nil, err
	}
	workers, err := s.workers.Status(ctx)
	if err != nil {
		result.OwnerError = err.Error()
	} else {
		result.Workers, result.OwnerState = workers, "ready"
		scheduling, err := s.workers.Scheduler().Snapshot(ctx)
		if err != nil {
			result.OwnerState, result.OwnerError = "unavailable", err.Error()
		} else {
			policy := scheduling.Policy
			result.Resources = scheduling.Resources
			result.Admission = Policy{QueueCapacity: policy.QueueCapacity, GlobalLimit: policy.GlobalLimit, ForegroundBurst: policy.ForegroundBurst, MaxQueueWait: policy.MaxQueueWait.String()}
		}
	}
	return result, nil
}

// PolicySource reads the client's explicit scheduling preference without changing the owner.
func PolicySource(db libdbexec.DBManager) func(context.Context) (admission.Config, bool, error) {
	return func(ctx context.Context) (admission.Config, bool, error) {
		if db == nil {
			return admission.Config{}, false, errors.New("scheduling settings require persistence")
		}
		var raw string
		err := runtimetypes.New(db.WithoutTransaction()).GetKV(ctx, clikv.Prefix+"inference.modeld.admission", &raw)
		if errors.Is(err, libdbexec.ErrNotFound) {
			return admission.Config{}, false, nil
		}
		if err != nil {
			return admission.Config{}, false, err
		}
		var policy admission.Config
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			return policy, true, err
		}
		return policy, true, nil
	}
}

// ReadOptions reads and validates a role's persisted worker settings without starting local inference.
func ReadOptions(ctx context.Context, store runtimetypes.Store, role string) (modeldconn.WorkerOptions, error) {
	if store == nil {
		return modeldconn.WorkerOptions{}, errors.New("worker settings require persistence")
	}
	if role == "alternative" {
		var mode, model string
		if err := store.GetKV(ctx, clikv.Prefix+"inference.assignment.alternative", &mode); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
			return modeldconn.WorkerOptions{}, err
		}
		if mode == "" {
			if err := store.GetKV(ctx, clikv.Prefix+"default-alt-model", &model); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
				return modeldconn.WorkerOptions{}, err
			}
		}
		if mode == "inherited" || mode == "" && model == "" {
			role = "primary"
		}
	}
	return ReadAssignedOptions(ctx, store, role)
}

// ReadAssignedOptions reads a role's own execution settings when reviewing an independent assignment.
func ReadAssignedOptions(ctx context.Context, store runtimetypes.Store, role string) (modeldconn.WorkerOptions, error) {
	if store == nil {
		return modeldconn.WorkerOptions{}, errors.New("worker settings require persistence")
	}
	var raw string
	if err := store.GetKV(ctx, clikv.Prefix+"inference.modeld."+role, &raw); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
		return modeldconn.WorkerOptions{}, err
	}
	var options modeldconn.WorkerOptions
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			return options, err
		}
	}
	return options, options.Validate()
}

// InventoryRequest selects this installation or one saved modeld connection.
type InventoryRequest struct {
	Target       string `json:"target"`
	ConnectionID string `json:"connectionId"`
}

// Inventory reads capabilities from the selected execution host without loading a model.
func (s *Service) Inventory(ctx context.Context, req InventoryRequest) (transport.Inventory, error) {
	switch req.Target {
	case "local":
		if req.ConnectionID != "" {
			return transport.Inventory{}, errors.New("local inventory cannot select a connection")
		}
		return s.workers.Inventory(ctx)
	case "connection":
		if req.ConnectionID == "" {
			return transport.Inventory{}, errors.New("select a connection for native inventory")
		}
		backend, err := backendservice.New(s.db).Get(ctx, req.ConnectionID)
		if err != nil {
			return transport.Inventory{}, err
		}
		if backend.Type != "modeld" && backend.Type != "local" {
			return transport.Inventory{}, errors.New("selected connection does not expose native device inventory")
		}
		if backend.BaseURL == modeldconn.LocalSentinel {
			return s.workers.Inventory(ctx)
		}
		if backend.BaseURL == "" {
			return transport.Inventory{}, errors.New("configure the modeld endpoint before reading its devices")
		}
		client, err := modeldconn.Endpoint(ctx, "inventory:"+backend.ID, backend.BaseURL)
		if err != nil {
			return transport.Inventory{}, err
		}
		return client.Inventory(ctx)
	default:
		return transport.Inventory{}, errors.New("inventory requires a local or connection target")
	}
}

// Inspect reads one existing worker without launching or loading another allocation.
func (s *Service) Inspect(ctx context.Context, instance string) (*Inspection, error) {
	result, err := s.workers.InspectWorker(ctx, instance)
	return &result, err
}

// InspectWorker reads a selected worker's native status and capacity.
func InspectWorker(ctx context.Context, worker modeldconn.WorkerStatus) (*Inspection, error) {
	result, err := modeldconn.InspectOwnedWorker(ctx, worker)
	return &result, err
}

// NewConfigured creates a runtime client that reads worker preferences from the local database.
func NewConfigured(ctx context.Context, db libdbexec.DBManager) (*modeldconn.Workers, error) {
	workers, err := modeldconn.NewConfiguredWorkers(ctx, modeldconn.DataRootFromContext(ctx), func(ctx context.Context, role string) (modeldconn.WorkerOptions, error) {
		return ReadOptions(ctx, runtimetypes.New(db.WithoutTransaction()), role)
	})
	if err != nil {
		return nil, err
	}
	if err = workers.SetPolicySource(PolicySource(db)); err != nil {
		return nil, errors.Join(err, workers.Close())
	}
	if err = workers.SetCompletionSource(CompletionSource(db)); err != nil {
		return nil, errors.Join(err, workers.Close())
	}
	return workers, nil
}
