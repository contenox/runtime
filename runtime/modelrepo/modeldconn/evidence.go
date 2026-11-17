package modeldconn

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

type captureKey struct{}

// ExecutionRevision reports resolved native settings that require another explicit review.
type ExecutionRevision struct {
	Role      string
	Execution transport.SessionExecution
}

// Error names the review required before changed native settings can be applied.
func (r *ExecutionRevision) Error() string {
	return "native execution resolved differently from the reviewed settings; " + remedy.ReplanRole
}

// ExecutionCapture collects native allocation evidence for one verification operation.
type ExecutionCapture struct {
	mu      sync.Mutex
	reports []transport.SessionExecution
}

// CaptureExecution scopes native evidence to an operation without changing live role settings.
func CaptureExecution(ctx context.Context) (context.Context, *ExecutionCapture) {
	capture := &ExecutionCapture{}
	return context.WithValue(ctx, captureKey{}, capture), capture
}

// CapturingExecution reports whether the current operation requires native allocation evidence.
func CapturingExecution(ctx context.Context) bool {
	_, ok := ctx.Value(captureKey{}).(*ExecutionCapture)
	return ok
}

// Reports returns the native allocations opened during the captured operation.
func (c *ExecutionCapture) Reports() []transport.SessionExecution {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) == 0 {
		return nil
	}
	results := make([]transport.SessionExecution, len(c.reports))
	for index, report := range c.reports {
		results[index] = *transport.CopyExecutionReport(report)
	}
	return results
}

// RecordExecution requires successful native evidence when an operation is capturing it.
func RecordExecution(ctx context.Context, model ModelRef, report *transport.SessionExecution) error {
	capture, capturing := ctx.Value(captureKey{}).(*ExecutionCapture)
	if !capturing {
		return nil
	}
	if report == nil {
		return errors.New("native execution evidence is unavailable; " + remedy.UpdateLocal)
	}
	if report.ModelName != model.Name || report.Backend != model.Type || report.Digest == "" || (model.Digest != "" && report.Digest != model.Digest) {
		return fmt.Errorf("%w: native execution differs from the reviewed model; "+remedy.ReplanRole, transport.ErrDigestMismatch)
	}
	requested, resolved := report.Requested.Target.Normalize(), report.Resolved.Target.Normalize()
	expected, err := requestExecution(ctx, WorkerRole(ctx), model.Type)
	if err != nil {
		return err
	}
	if expected.Target != requested {
		return errors.New("native execution used different settings from the reviewed placement; " + remedy.ReplanRole)
	}
	if err := requested.Validate(); err != nil {
		return err
	}
	if err := resolved.Validate(); err != nil {
		return err
	}
	if resolved.Mode == transport.PlacementAutomatic {
		return errors.New("native execution did not report its resolved device; " + remedy.UpdateLocal)
	}
	if requested.Mode != transport.PlacementAutomatic && requested != resolved || expected.Context > 0 && expected.Context != report.Resolved.NumCtx || expected.Threads > 0 && expected.Threads != report.Resolved.NumThreads || expected.Batch > 0 && expected.Batch != report.Resolved.NumBatch || expected.KVCacheType != "" && expected.KVCacheType != report.Resolved.KVCacheType {
		return &ExecutionRevision{Role: WorkerRole(ctx), Execution: *transport.CopyExecutionReport(*report)}
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.reports = append(capture.reports, *transport.CopyExecutionReport(*report))
	return nil
}
