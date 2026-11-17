package modeldconn

import (
	"context"
	"errors"

	"github.com/contenox/runtime/internal/transport"
)

// CompletionPlan binds verified completion residency to the host that owns its resources.
type CompletionPlan struct {
	Endpoint   string                         `json:"endpoint"`
	Protection transport.CompletionProtection `json:"protection"`
}

// SetCompletionSource reads committed completion protection before other native allocations.
func (m *Workers) SetCompletionSource(source func(context.Context, string) (CompletionPlan, error)) error {
	if source == nil {
		return errors.New("completion protection source is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadCompletion = source
	return nil
}

func completionProtection(ctx context.Context, endpoint string) (transport.CompletionProtection, error) {
	workers := WorkersFromContext(ctx)
	if workers == nil {
		return transport.CompletionProtection{State: "unconfigured"}, nil
	}
	workers.mu.Lock()
	source := workers.loadCompletion
	workers.mu.Unlock()
	plan, err := source(ctx, endpoint)
	if err != nil {
		return transport.CompletionProtection{}, err
	}
	if plan.Protection.State == "protected" && plan.Endpoint != endpoint {
		return transport.CompletionProtection{State: "external"}, nil
	}
	return plan.Protection, nil
}

func protectExecution(ctx context.Context, endpoint string, cfg *transport.Config) error {
	if cfg.Execution == nil || cfg.Execution.Completion.State != "" {
		return nil
	}
	protection, err := completionProtection(ctx, endpoint)
	if err != nil {
		return err
	}
	execution := *cfg.Execution
	execution.Completion = protection
	cfg.Execution = &execution
	return nil
}
