package modeldconn

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modeldprobe"
)

func (m *Workers) sharedEndpoint(ctx context.Context) (EndpointClient, error) {
	st := modeldprobe.New(m.root).Probe(ctx)
	if err := st.Err(); err != nil {
		return EndpointClient{}, err
	}
	if st.Backend != "shared" {
		return EndpointClient{}, errors.New("local inference requires an updated shared modeld owner; " + remedy.UpdateLocal)
	}
	return Endpoint(ctx, m.owner, st.Endpoint)
}

type sharedScheduler struct{ workers *Workers }

// SetPolicySource checks a client's saved scheduling policy against the shared owner before execution.
func (m *Workers) SetPolicySource(source func(context.Context) (admission.Config, bool, error)) error {
	if source == nil {
		return errors.New("scheduling policy source is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadPolicy = source
	return nil
}

func (s sharedScheduler) Register(context.Context, admission.Worker) error {
	return errors.New("only the shared owner may register native workers")
}

func (s sharedScheduler) Update(context.Context, admission.Worker) error {
	return errors.New("only the shared owner may update native worker state")
}

func (s sharedScheduler) Acquire(context.Context, admission.Request) (admission.Permit, error) {
	return admission.Permit{}, errors.New("shared admission is acquired through the native operation")
}

func (s sharedScheduler) WaitCancellation(context.Context, admission.Permit) error {
	return errors.New("shared cancellation is observed by the native operation owner")
}

func (s sharedScheduler) Release(context.Context, admission.Permit) error {
	return errors.New("shared admission is released after the native operation ends")
}

func (s sharedScheduler) Snapshot(ctx context.Context) (admission.Snapshot, error) {
	client, err := s.workers.sharedEndpoint(ctx)
	if err != nil {
		return admission.Snapshot{}, err
	}
	return client.Admission(ctx)
}

func (s sharedScheduler) Configure(ctx context.Context, policy admission.Config) error {
	client, err := s.workers.sharedEndpoint(ctx)
	if err != nil {
		return err
	}
	return client.ConfigureAdmission(ctx, policy)
}

// ConfigureCeilings replaces the shared owner's device ceilings.
func (s sharedScheduler) ConfigureCeilings(ctx context.Context, ceilings map[string]float64) error {
	client, err := s.workers.sharedEndpoint(ctx)
	if err != nil {
		return err
	}
	return client.ConfigureCeilings(ctx, ceilings)
}

func requestExecution(ctx context.Context, role, backend string) (*transport.Execution, error) {
	options := WorkerOptions{}
	if proposed, ok := ProposedWorkerOptions(ctx, role); ok {
		options = proposed
	} else if workers := WorkersFromContext(ctx); workers != nil {
		var err error
		options, err = workers.roleOptions(ctx, role)
		if err != nil {
			return nil, err
		}
	}
	if options.Backend != "" && options.Backend != backend {
		return nil, fmt.Errorf("selected model requires %s, but %s is configured for %s", backend, role, options.Backend)
	}
	qos := admissionOptions(ctx)
	if role == "embedding" || role == "inspection" {
		qos.Priority = admission.Background
	}
	return &transport.Execution{Target: options.Target.Normalize(), Role: role, Priority: string(qos.Priority), RequesterID: qos.RequesterID, Supersede: qos.Supersede,
		Context: options.Context, Threads: options.Threads, Batch: options.Batch, KVCacheType: options.KVCacheType,
		Memory: options.Memory, Reserve: options.Reserve, ColdMemory: options.ColdMemory, IdleTTL: options.IdleTTL,
		Headroom: options.Headroom, ComputeReserve: options.ComputeReserve, MinHotContext: options.MinHotContext}, nil
}

func (m *Workers) acquireShared(ctx context.Context, spec WorkerSpec) (ModeldTarget, func() error, error) {
	m.mu.Lock()
	closed := m.closed || m.ctx.Err() != nil
	m.mu.Unlock()
	if closed {
		return ModeldTarget{}, nil, errors.New("local inference client is closed")
	}
	st, err := readyDaemonWithProbe(ctx, modeldprobe.New(m.root))
	if err != nil {
		return ModeldTarget{}, nil, err
	}
	if st.Backend != "shared" {
		if st.Backend != spec.Backend {
			return ModeldTarget{}, nil, fmt.Errorf("model %s requires %s, but the standalone owner serves %s", spec.Model, spec.Backend, st.Backend)
		}
		return ModeldTarget{leaseRoot: m.root, BackendID: m.owner, Endpoint: st.Endpoint, Instance: st.Instance, standaloneConfig: &spec.Config}, func() error { return nil }, nil
	}
	m.mu.Lock()
	loadPolicy := m.loadPolicy
	m.mu.Unlock()
	expected, configured, err := loadPolicy(ctx)
	if err != nil {
		return ModeldTarget{}, nil, err
	}
	if configured {
		actual, err := m.Scheduler().Snapshot(ctx)
		if err != nil {
			return ModeldTarget{}, nil, err
		}
		if actual.Policy != expected {
			return ModeldTarget{}, nil, errors.New("saved scheduling settings differ from the shared owner; " + remedy.SaveScheduling + " before local execution")
		}
	}
	qos := admissionOptions(ctx)
	execution := &transport.Execution{
		Context: spec.Config.NumCtx, Threads: spec.Config.NumThreads, Batch: spec.Config.NumBatch, KVCacheType: spec.Config.KVCacheType,
		Target: spec.Config.Target.Normalize(),
		Role:   spec.Role, Priority: string(qos.Priority), RequesterID: qos.RequesterID, Supersede: qos.Supersede,
		Memory: spec.Memory, Reserve: spec.Reserve, ColdMemory: spec.ColdMemory, IdleTTL: spec.IdleTTL,
		Headroom: spec.Headroom, ComputeReserve: spec.ComputeReserve, MinHotContext: spec.MinHotContext,
	}
	execution.Completion, err = completionProtection(WithWorkers(ctx, m), LocalSentinel)
	if err != nil {
		return ModeldTarget{}, nil, err
	}
	return ModeldTarget{leaseRoot: m.root, BackendID: m.owner, Endpoint: st.Endpoint, Instance: st.Instance, Execution: execution}, func() error { return nil }, nil
}
