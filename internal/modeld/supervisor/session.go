package supervisor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
)

type sessionControl struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
}

func watchPermit(ctx context.Context, scheduler admission.Service, permit admission.Permit) *sessionControl {
	lifetime, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	control := &sessionControl{ctx: lifetime, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(control.done)
		err := scheduler.WaitCancellation(lifetime, permit)
		if err == nil {
			err = transport.ErrSessionClosed
		}
		cancel(err)
	}()
	return control
}

func (s *sessionControl) cause() error { return context.Cause(s.ctx) }

func (s *sessionControl) operation(ctx context.Context) (context.Context, func()) {
	operation, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(s.ctx, func() { cancel(s.cause()) })
	if s.ctx.Err() != nil {
		cancel(s.cause())
	}
	return operation, func() { stop(); cancel(context.Canceled) }
}

func (s *sessionControl) close() {
	s.cancel(transport.ErrSessionClosed)
	<-s.done
}

type session struct {
	transport.Session
	requested  transport.Config
	owner      string
	worker     string
	allocation *transport.NativeAllocation
	control    *sessionControl
	once       sync.Once
	release    func() error
	retire     func() error
	err        error
}

func (s *session) EnsurePrefix(ctx context.Context, input transport.PrefixInput) (transport.PrefixStatus, error) {
	ctx, finish := s.control.operation(ctx)
	defer finish()
	if err := ctx.Err(); err != nil {
		return transport.PrefixStatus{}, errors.Join(s.control.cause(), err)
	}
	result, err := s.Session.EnsurePrefix(ctx, input)
	return result, errors.Join(s.control.cause(), err)
}

func (s *session) PrefillSuffix(ctx context.Context, input transport.SuffixInput) (transport.SuffixStatus, error) {
	ctx, finish := s.control.operation(ctx)
	defer finish()
	if err := ctx.Err(); err != nil {
		return transport.SuffixStatus{}, errors.Join(s.control.cause(), err)
	}
	result, err := s.Session.PrefillSuffix(ctx, input)
	return result, errors.Join(s.control.cause(), err)
}

func (s *session) Snapshot(ctx context.Context) (transport.SessionSnapshot, error) {
	ctx, finish := s.control.operation(ctx)
	defer finish()
	if err := ctx.Err(); err != nil {
		return transport.SessionSnapshot{}, errors.Join(s.control.cause(), err)
	}
	result, err := s.Session.Snapshot(ctx)
	return result, errors.Join(s.control.cause(), err)
}

func (s *session) Restore(ctx context.Context, snapshot transport.SessionSnapshot) error {
	ctx, finish := s.control.operation(ctx)
	defer finish()
	if err := ctx.Err(); err != nil {
		return errors.Join(s.control.cause(), err)
	}
	err := s.Session.Restore(ctx, snapshot)
	return errors.Join(s.control.cause(), err)
}

func (s *session) ExplainContext() transport.ContextReport {
	if err := s.control.cause(); err != nil {
		return transport.ContextReport{Closed: true, FatalError: err.Error()}
	}
	report := s.Session.ExplainContext()
	if report.Execution != nil {
		report.Execution.Requested = s.requested
		report.Execution.OwnerEpoch, report.Execution.WorkerIdentity = s.owner, s.worker
		if s.allocation != nil && report.Execution.Capacity.Allocation != nil {
			report.Execution.Capacity.Allocation.Resources.Identity = s.allocation.Resources.Identity
		}
		report.Execution = transport.CopyExecutionReport(*report.Execution)
	}
	return report
}

func (s *session) Decode(ctx context.Context, config transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	operation, finish := s.control.operation(ctx)
	if err := operation.Err(); err != nil {
		finish()
		return nil, errors.Join(s.control.cause(), err)
	}
	chunks, err := s.Session.Decode(operation, config)
	if err != nil {
		finish()
		return nil, errors.Join(s.control.cause(), err)
	}
	out := make(chan transport.StreamChunk, 1)
	go func() {
		defer close(out)
		defer finish()
		failed := false
		for chunk := range chunks {
			if chunk.Error != nil {
				chunk.Error = errors.Join(s.control.cause(), chunk.Error)
				failed = true
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
		if err := s.control.cause(); err != nil && !failed {
			select {
			case out <- transport.StreamChunk{Error: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

func (s *session) Close() error {
	s.once.Do(func() {
		s.control.close()
		closed := make(chan error, 1)
		go func() { closed <- s.Session.Close() }()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		select {
		case s.err = <-closed:
		case <-timeout.C:
			s.err = errors.New("native session did not stop after cancellation")
		}
		if s.err != nil {
			if err := s.retire(); err != nil {
				s.err = errors.Join(s.err, err)
				return
			}
		}
		s.err = errors.Join(s.err, s.release())
	})
	return s.err
}
