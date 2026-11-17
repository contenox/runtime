package modeldconn

import (
	"context"
	"github.com/contenox/runtime/internal/modeld/admission"
)

type workerContextKey struct{}
type roleContextKey struct{}
type rootContextKey struct{}
type admissionContextKey struct{}

// AdmissionOptions identifies QoS and supersession without changing model routing.
type AdmissionOptions struct {
	Priority    admission.Priority
	RequesterID string
	Supersede   bool
}

// WithAdmissionOptions supplies request-scoped scheduling metadata.
func WithAdmissionOptions(ctx context.Context, options AdmissionOptions) context.Context {
	return context.WithValue(ctx, admissionContextKey{}, options)
}

func admissionOptions(ctx context.Context) AdmissionOptions {
	if options, ok := ctx.Value(admissionContextKey{}).(AdmissionOptions); ok {
		return options
	}
	return AdmissionOptions{Priority: admission.Foreground}
}

// WithDataRoot binds discovery and model acquisition to a runtime's configured root.
func WithDataRoot(ctx context.Context, root string) context.Context {
	if root == "" {
		root = DataRoot()
	}
	return context.WithValue(ctx, rootContextKey{}, root)
}

// DataRootFromContext returns the worker manager's root or the configured discovery root.
func DataRootFromContext(ctx context.Context) string {
	if workers := WorkersFromContext(ctx); workers != nil {
		return workers.root
	}
	if root, ok := ctx.Value(rootContextKey{}).(string); ok {
		return root
	}
	return DataRoot()
}

// WithWorkers supplies the runtime-owned native worker manager.
func WithWorkers(ctx context.Context, workers *Workers) context.Context {
	if workers == nil {
		return ctx
	}
	return context.WithValue(ctx, workerContextKey{}, workers)
}

// WithWorkerRole identifies the inference role without changing model selection.
func WithWorkerRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleContextKey{}, role)
}

// WorkersFromContext returns the runtime-owned manager, if present.
func WorkersFromContext(ctx context.Context) *Workers {
	m, _ := ctx.Value(workerContextKey{}).(*Workers)
	return m
}

// WorkerRole returns the explicit inference role, or primary for ordinary calls.
func WorkerRole(ctx context.Context) string {
	if role, ok := ctx.Value(roleContextKey{}).(string); ok {
		return role
	}
	return "primary"
}
