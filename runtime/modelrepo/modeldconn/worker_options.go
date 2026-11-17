package modeldconn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
)

// WorkerOptions configures one role's managed local process.
type WorkerOptions struct {
	// SchedulingMode records whether the reviewed role requires protection or permits serialized execution.
	SchedulingMode string                    `json:"scheduling_mode,omitempty"`
	Context        int                       `json:"context,omitempty"`
	Threads        int                       `json:"threads,omitempty"`
	Batch          int                       `json:"batch,omitempty"`
	KVCacheType    string                    `json:"kv_cache_type,omitempty"`
	Residency      string                    `json:"residency,omitempty"`
	Target         transport.ExecutionTarget `json:"target"`
	Backend        string                    `json:"backend,omitempty"`
	Memory         string                    `json:"memory,omitempty"`
	Reserve        string                    `json:"reserve,omitempty"`
	ColdMemory     string                    `json:"cold_memory,omitempty"`
	IdleTTL        string                    `json:"idle_ttl,omitempty"`
	Headroom       string                    `json:"headroom,omitempty"`
	ComputeReserve string                    `json:"compute_reserve,omitempty"`
	MinHotContext  string                    `json:"min_hot_context,omitempty"`
}

// Validate checks native selection and launch arguments without starting a worker.
func (o WorkerOptions) Validate() error {
	if o.SchedulingMode != "" && o.SchedulingMode != "protected" && o.SchedulingMode != "serialized" {
		return errors.New("reviewed scheduling requires protected or serialized execution")
	}
	if o.Context < 0 || o.Threads < 0 || o.Batch < 0 {
		return errors.New("context, threads and batch must be nonnegative; zero selects the backend policy")
	}
	if o.KVCacheType != "" && o.KVCacheType != "f16" && o.KVCacheType != "q8_0" && o.KVCacheType != "q4_0" {
		return errors.New("KV cache type requires f16, q8_0 or q4_0")
	}
	if o.Residency != "" && o.Residency != "protected" && o.Residency != "on-demand" {
		return errors.New("completion residency requires protected or on-demand")
	}
	if err := o.Target.Validate(); err != nil {
		return err
	}
	if _, err := (capacity.Policy{}).WithHeadroom(o.Headroom); err != nil {
		return err
	}
	if o.ComputeReserve != "" {
		if _, err := capacity.ParseMemorySetting(o.ComputeReserve); err != nil {
			return err
		}
	}
	if o.MinHotContext != "" && o.MinHotContext != "auto" {
		value, err := strconv.Atoi(o.MinHotContext)
		if err != nil || value < 0 {
			return fmt.Errorf("minimum hot context requires auto or a nonnegative token count")
		}
	}
	if o.Backend != "" && o.Backend != "llama" && o.Backend != "openvino" {
		return fmt.Errorf("invalid native backend %q", o.Backend)
	}
	if _, err := (capacity.Policy{}).WithMemorySettings(o.Memory, o.Reserve, o.ColdMemory); err != nil {
		return err
	}
	if o.IdleTTL != "" && o.IdleTTL != "off" && o.IdleTTL != "0" {
		d, err := time.ParseDuration(o.IdleTTL)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid idle TTL %q", o.IdleTTL)
		}
	}
	return nil
}

// SetOptions changes the launch configuration used by the role's next request.
func (m *Workers) SetOptions(role string, options WorkerOptions) error {
	if !validWorkerRole(role) {
		return fmt.Errorf("invalid modeld role %q", role)
	}
	if err := options.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shared {
		delete(m.options, role)
		return nil
	}
	m.options[role] = options
	return nil
}

func (m *Workers) roleOptions(ctx context.Context, role string) (WorkerOptions, error) {
	if options, ok := ProposedWorkerOptions(ctx, role); ok {
		return options, nil
	}
	m.mu.Lock()
	options, explicit := m.options[role]
	m.mu.Unlock()
	if explicit {
		return options, nil
	}
	options, err := m.loadOptions(ctx, role)
	if err != nil {
		return WorkerOptions{}, fmt.Errorf("modeld %s settings: %w", role, err)
	}
	return options, options.Validate()
}

type proposedWorkerOptionsKey struct{}

// WithWorkerOptions scopes proposed settings to verification without changing live defaults.
func WithWorkerOptions(ctx context.Context, role string, options WorkerOptions) (context.Context, error) {
	if !validWorkerRole(role) || role == "inspection" {
		return nil, fmt.Errorf("invalid inference role %q", role)
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	options.Target = options.Target.Normalize()
	values := map[string]WorkerOptions{}
	if previous, ok := ctx.Value(proposedWorkerOptionsKey{}).(map[string]WorkerOptions); ok {
		for key, value := range previous {
			values[key] = value
		}
	}
	values[role] = options
	return context.WithValue(ctx, proposedWorkerOptionsKey{}, values), nil
}

// ProposedWorkerOptions returns request-scoped settings when a route is being verified.
func ProposedWorkerOptions(ctx context.Context, role string) (WorkerOptions, bool) {
	values, _ := ctx.Value(proposedWorkerOptionsKey{}).(map[string]WorkerOptions)
	options, ok := values[role]
	return options, ok
}

// Options reads each role's configured launch options without starting workers.
func (m *Workers) Options(ctx context.Context) (map[string]WorkerOptions, error) {
	out := make(map[string]WorkerOptions)
	for _, role := range []string{"primary", "alternative", "autocomplete", "next-edit", "embedding"} {
		options, err := m.roleOptions(ctx, role)
		if err != nil {
			return nil, err
		}
		out[role] = options
	}
	return out, nil
}

func validWorkerRole(role string) bool {
	return role == "primary" || role == "alternative" || role == "autocomplete" || role == "next-edit" || role == "embedding" || role == "inspection"
}
