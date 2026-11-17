package owner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/internal/transport/grpc"
)

type computePermitKey struct{}

type computeGate struct {
	ctx     context.Context
	cancel  context.CancelFunc
	client  *grpc.Client
	permit  admission.Permit
	mu      sync.Mutex
	active  int
	paused  chan struct{}
	err     error
	pending atomic.Bool
	wake    chan struct{}
	done    chan struct{}
}

func newComputeGate(ctx context.Context, client *grpc.Client, permit admission.Permit) (*computeGate, error) {
	if permit.ID == 0 || permit.OwnerEpoch == "" || permit.WorkerID == "" {
		return nil, errors.New("managed execution requires its owner admission permit; update local inference")
	}
	lifetime, cancel := context.WithCancel(ctx)
	gate := &computeGate{ctx: lifetime, cancel: cancel, client: client, permit: permit, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go gate.watch()
	return gate, nil
}

func (g *computeGate) signal() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

func (g *computeGate) fail(err error) {
	g.mu.Lock()
	g.err = err
	g.pending.Store(true)
	g.mu.Unlock()
	g.signal()
}

func (g *computeGate) watch() {
	defer close(g.done)
	for {
		if err := g.client.WaitYield(g.ctx, g.permit); err != nil {
			if g.ctx.Err() == nil {
				g.fail(err)
			}
			return
		}
		g.pending.Store(true)
		for g.pending.Load() {
			g.mu.Lock()
			idle := g.active == 0
			g.mu.Unlock()
			if idle {
				if err := g.checkpoint(g.ctx); err != nil {
					g.fail(err)
					return
				}
				break
			}
			select {
			case <-g.ctx.Done():
				return
			case <-g.wake:
			}
		}
	}
}

func (g *computeGate) checkpoint(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !g.pending.Load() {
		return g.ctx.Err()
	}
	g.mu.Lock()
	if g.err != nil {
		err := g.err
		g.mu.Unlock()
		return err
	}
	if g.paused != nil {
		resumed := g.paused
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-g.ctx.Done():
			return g.ctx.Err()
		case <-resumed:
			return g.checkpoint(ctx)
		}
	}
	g.paused = make(chan struct{})
	g.mu.Unlock()
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	err := g.client.ResumeAtBoundary(operation, g.permit)
	stop()
	cancel()
	g.mu.Lock()
	g.err = err
	g.pending.Store(err != nil)
	close(g.paused)
	g.paused = nil
	g.mu.Unlock()
	g.signal()
	return err
}

func (g *computeGate) operation(ctx context.Context) (context.Context, func(), error) {
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	g.mu.Lock()
	g.active++
	g.mu.Unlock()
	finish := func() {
		stop()
		cancel()
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
		g.signal()
	}
	operation = context.WithValue(operation, computePermitKey{}, g.permit)
	operation, err := sessionkit.WithNativeBoundary(operation, g.checkpoint)
	if err != nil {
		finish()
		return nil, nil, err
	}
	if err := g.checkpoint(operation); err != nil {
		finish()
		return nil, nil, err
	}
	return operation, finish, nil
}

func (g *computeGate) close() {
	g.cancel()
	<-g.done
}

type managedSlot struct {
	*slot.Service
	lifetime context.Context
	client   *grpc.Client
}

func (s *managedSlot) LoadModel(ctx context.Context, request transport.LoadModelRequest) (transport.ActiveModel, error) {
	gate, err := newComputeGate(s.lifetime, s.client, request.Config.AdmissionPermit)
	if err != nil {
		return transport.ActiveModel{}, err
	}
	defer gate.close()
	request.Config.AdmissionPermit = admission.Permit{}
	operation, finish, err := gate.operation(ctx)
	if err != nil {
		return transport.ActiveModel{}, err
	}
	defer finish()
	return s.Service.LoadModel(operation, request)
}

func (s *managedSlot) OpenSession(ctx context.Context, request transport.OpenSessionRequest) (transport.Session, error) {
	gate, err := newComputeGate(s.lifetime, s.client, request.Config.AdmissionPermit)
	if err != nil {
		return nil, err
	}
	request.Config.AdmissionPermit = admission.Permit{}
	operation, finish, err := gate.operation(ctx)
	if err != nil {
		gate.close()
		return nil, err
	}
	session, err := s.Service.OpenSession(operation, request)
	finish()
	if err != nil {
		gate.close()
		return nil, err
	}
	return &computeSession{Session: session, gate: gate}, nil
}

func (s *managedSlot) Embed(ctx context.Context, request transport.EmbedRequest) (transport.EmbedResult, error) {
	gate, err := newComputeGate(s.lifetime, s.client, request.Config.AdmissionPermit)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	defer gate.close()
	request.Config.AdmissionPermit = admission.Permit{}
	operation, finish, err := gate.operation(ctx)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	defer finish()
	return s.Service.Embed(operation, request)
}

func (s *managedSlot) Describe(ctx context.Context, request transport.OpenSessionRequest) (transport.ModelInfo, error) {
	request.Config.AdmissionPermit = admission.Permit{}
	return s.Service.Describe(ctx, request)
}

type computeSession struct {
	transport.Session
	gate *computeGate
	once sync.Once
	err  error
}

func (s *computeSession) EnsurePrefix(ctx context.Context, input transport.PrefixInput) (transport.PrefixStatus, error) {
	operation, finish, err := s.gate.operation(ctx)
	if err != nil {
		return transport.PrefixStatus{}, err
	}
	defer finish()
	return s.Session.EnsurePrefix(operation, input)
}

func (s *computeSession) PrefillSuffix(ctx context.Context, input transport.SuffixInput) (transport.SuffixStatus, error) {
	operation, finish, err := s.gate.operation(ctx)
	if err != nil {
		return transport.SuffixStatus{}, err
	}
	defer finish()
	return s.Session.PrefillSuffix(operation, input)
}

func (s *computeSession) Snapshot(ctx context.Context) (transport.SessionSnapshot, error) {
	operation, finish, err := s.gate.operation(ctx)
	if err != nil {
		return transport.SessionSnapshot{}, err
	}
	defer finish()
	return s.Session.Snapshot(operation)
}

func (s *computeSession) Restore(ctx context.Context, snapshot transport.SessionSnapshot) error {
	operation, finish, err := s.gate.operation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return s.Session.Restore(operation, snapshot)
}

func (s *computeSession) Decode(ctx context.Context, config transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	operation, finish, err := s.gate.operation(ctx)
	if err != nil {
		return nil, err
	}
	chunks, err := s.Session.Decode(operation, config)
	if err != nil {
		finish()
		return nil, err
	}
	out := make(chan transport.StreamChunk, 1)
	go func() {
		defer close(out)
		defer finish()
		for chunk := range chunks {
			select {
			case out <- chunk:
			case <-operation.Done():
			}
		}
	}()
	return out, nil
}

func (s *computeSession) Close() error {
	s.once.Do(func() {
		s.gate.close()
		s.err = s.Session.Close()
	})
	return s.err
}
