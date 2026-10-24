package nativeturn

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/contenox/runtime/libacp"
)

var ErrClosed = errors.New("nativeturn: registry is closed")

type Registry struct {
	cfg Config

	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu       sync.Mutex
	sessions map[libacp.SessionID]*turnSession
	closed   bool
}

func New(cfg Config) *Registry {
	if cfg.JournalSize <= 0 {
		cfg.JournalSize = DefaultJournalSize
	}
	if cfg.TurnDeadline <= 0 {
		cfg.TurnDeadline = DefaultTurnDeadline
	}
	if cfg.GraceWindow <= 0 {
		cfg.GraceWindow = DefaultGraceWindow
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Registry{
		cfg:        cfg,
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
		sessions:   make(map[libacp.SessionID]*turnSession),
	}
}

type Turn struct {
	ts *turnSession
}

func (t *Turn) SessionID() libacp.SessionID { return t.ts.sessionID }

func (t *Turn) Done() <-chan struct{} { return t.ts.done }

func (t *Turn) Result() Result {
	t.ts.mu.Lock()
	defer t.ts.mu.Unlock()
	return t.ts.result
}

func (t *Turn) Await(ctx context.Context) (Result, bool) {
	select {
	case <-t.ts.done:
		return t.Result(), true
	case <-ctx.Done():
		return Result{}, false
	}
}

func (t *Turn) Detach(viewerID string) { t.ts.detach(viewerID) }

func (t *Turn) Cancel() { t.ts.teardown() }

func (r *Registry) Start(sid libacp.SessionID, fn TurnFunc, viewer Viewer) (*Turn, bool, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, ErrClosed
	}
	if ts, ok := r.sessions[sid]; ok && !ts.isFinished() {
		r.mu.Unlock()
		if err := ts.attach(context.Background(), viewer); err != nil {
			return nil, false, err
		}
		return &Turn{ts: ts}, false, nil
	}
	ts := r.newTurnSession(sid)
	r.sessions[sid] = ts
	r.mu.Unlock()

	_ = ts.attach(context.Background(), viewer)
	go ts.run(fn)
	return &Turn{ts: ts}, true, nil
}

func (r *Registry) AttachIfRunning(ctx context.Context, sid libacp.SessionID, viewer Viewer) (*Turn, bool, error) {
	r.mu.Lock()
	ts, ok := r.sessions[sid]
	r.mu.Unlock()
	if !ok || ts.isFinished() {
		return nil, false, nil
	}
	if err := ts.attach(ctx, viewer); err != nil {
		return nil, false, err
	}
	return &Turn{ts: ts}, true, nil
}

func (r *Registry) Cancel(sid libacp.SessionID) bool {
	r.mu.Lock()
	ts, ok := r.sessions[sid]
	r.mu.Unlock()
	if !ok {
		return false
	}
	ts.teardown()
	return true
}

func (r *Registry) Stop(sid libacp.SessionID) bool { return r.Cancel(sid) }

func (r *Registry) Get(sid libacp.SessionID) (TurnStatus, bool) {
	r.mu.Lock()
	ts, ok := r.sessions[sid]
	r.mu.Unlock()
	if !ok {
		return TurnStatus{}, false
	}
	return ts.status(), true
}

func (r *Registry) List() []TurnStatus {
	r.mu.Lock()
	live := make([]*turnSession, 0, len(r.sessions))
	for _, ts := range r.sessions {
		live = append(live, ts)
	}
	r.mu.Unlock()

	out := make([]TurnStatus, 0, len(live))
	for _, ts := range live {
		out = append(out, ts.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

func (r *Registry) ReapIdle(_ context.Context) error {
	r.mu.Lock()
	live := make([]*turnSession, 0, len(r.sessions))
	for _, ts := range r.sessions {
		live = append(live, ts)
	}
	r.mu.Unlock()

	now := time.Now()
	for _, ts := range live {
		ts.mu.Lock()
		finished := ts.finished
		viewers := len(ts.viewers)
		graceDeadline := ts.graceDeadline
		deadline := ts.deadline
		ts.mu.Unlock()

		reap := false
		switch {
		case finished && viewers == 0:
			reap = true
		case !finished && viewers == 0 && !graceDeadline.IsZero() && now.After(graceDeadline):
			reap = true
		case now.After(deadline.Add(r.cfg.GraceWindow)):
			reap = true
		}
		if reap {
			ts.teardown()
		}
	}
	return nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	live := make([]*turnSession, 0, len(r.sessions))
	for id, ts := range r.sessions {
		live = append(live, ts)
		delete(r.sessions, id)
	}
	r.mu.Unlock()

	for _, ts := range live {
		ts.teardown()
	}
	r.rootCancel()
	return nil
}

func (r *Registry) newTurnSession(sid libacp.SessionID) *turnSession {
	started := time.Now()
	turnCtx, cancel := context.WithDeadline(r.rootCtx, started.Add(r.cfg.TurnDeadline))
	return &turnSession{
		reg:       r,
		sessionID: sid,
		startedAt: started,
		deadline:  started.Add(r.cfg.TurnDeadline),
		turnCtx:   turnCtx,
		cancel:    cancel,
		done:      make(chan struct{}),
		journal:   newJournal(r.cfg.JournalSize),
		viewers:   make(map[string]Viewer),
	}
}

func (r *Registry) removeSession(sid libacp.SessionID, ts *turnSession) {
	r.mu.Lock()
	if cur, ok := r.sessions[sid]; ok && cur == ts {
		delete(r.sessions, sid)
	}
	r.mu.Unlock()
}
