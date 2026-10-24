package nativeturn

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/contenox/runtime/libacp"
)

const (
	StateRunning  = "running"
	StateGrace    = "grace"
	StateFinished = "finished"
)

type Result struct {
	StopReason libacp.StopReason
	Err        error
}

type TurnFunc func(ctx context.Context, emit func(ctx context.Context, n libacp.SessionNotification)) Result

type TurnStatus struct {
	SessionID libacp.SessionID `json:"sessionId"`
	StartedAt time.Time        `json:"startedAt"`
	Deadline  time.Time        `json:"deadline"`
	Viewers   int              `json:"viewers"`
	State     string           `json:"state"`
}

type turnSession struct {
	reg       *Registry
	sessionID libacp.SessionID
	startedAt time.Time
	deadline  time.Time

	turnCtx context.Context
	cancel  context.CancelFunc

	done   chan struct{}
	result Result

	reaping atomic.Bool

	mu            sync.Mutex
	seq           uint64
	journal       *journal
	viewers       map[string]Viewer
	order         []string
	finished      bool
	graceTimer    *time.Timer
	graceDeadline time.Time
}

func (ts *turnSession) emit(ctx context.Context, n libacp.SessionNotification) {
	ts.mu.Lock()
	ts.seq++
	ev := Event{Seq: ts.seq, Update: n}
	ts.journal.append(ev)
	for _, id := range ts.order {
		_ = ts.viewers[id].Deliver(ctx, ev)
	}
	ts.mu.Unlock()
}

func (ts *turnSession) attach(ctx context.Context, v Viewer) error {
	vid := v.ID()
	if vid == "" {
		return fmt.Errorf("nativeturn: viewer ID is required")
	}
	ts.mu.Lock()
	if _, dup := ts.viewers[vid]; dup {
		ts.mu.Unlock()
		return fmt.Errorf("nativeturn: viewer %q already attached to session %q", vid, ts.sessionID)
	}
	ts.viewers[vid] = v
	ts.order = append(ts.order, vid)
	if ts.graceTimer != nil {
		ts.graceTimer.Stop()
		ts.graceTimer = nil
	}
	ts.graceDeadline = time.Time{}
	for _, ev := range ts.journal.snapshot() {
		_ = v.Deliver(ctx, ev)
	}
	ts.mu.Unlock()
	return nil
}

func (ts *turnSession) detach(viewerID string) {
	ts.mu.Lock()
	if _, ok := ts.viewers[viewerID]; !ok {
		ts.mu.Unlock()
		return
	}
	delete(ts.viewers, viewerID)
	for i, id := range ts.order {
		if id == viewerID {
			ts.order = append(ts.order[:i], ts.order[i+1:]...)
			break
		}
	}
	empty := len(ts.viewers) == 0
	finished := ts.finished
	if empty && !finished {
		ts.graceDeadline = time.Now().Add(ts.reg.cfg.GraceWindow)
		if ts.graceTimer != nil {
			ts.graceTimer.Stop()
		}
		ts.graceTimer = time.AfterFunc(ts.reg.cfg.GraceWindow, ts.teardown)
	}
	ts.mu.Unlock()

	if empty && finished {
		ts.teardown()
	}
}

func (ts *turnSession) run(fn TurnFunc) {
	ts.markFinished(fn(ts.turnCtx, ts.emit))
}

func (ts *turnSession) markFinished(res Result) {
	ts.mu.Lock()
	if ts.finished {
		ts.mu.Unlock()
		return
	}
	ts.finished = true
	ts.result = res
	close(ts.done)
	viewers := len(ts.viewers)
	if ts.graceTimer != nil {
		ts.graceTimer.Stop()
		ts.graceTimer = nil
	}
	ts.graceDeadline = time.Time{}
	ts.mu.Unlock()

	ts.cancel()
	if viewers == 0 {
		ts.teardown()
	}
}

func (ts *turnSession) teardown() {
	if !ts.reaping.CompareAndSwap(false, true) {
		return
	}
	ts.cancel()
	ts.reg.removeSession(ts.sessionID, ts)
	ts.mu.Lock()
	if ts.graceTimer != nil {
		ts.graceTimer.Stop()
		ts.graceTimer = nil
	}
	ts.mu.Unlock()
}

func (ts *turnSession) isFinished() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.finished
}

func (ts *turnSession) status() TurnStatus {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	state := StateRunning
	switch {
	case ts.finished:
		state = StateFinished
	case len(ts.viewers) == 0:
		state = StateGrace
	}
	return TurnStatus{
		SessionID: ts.sessionID,
		StartedAt: ts.startedAt,
		Deadline:  ts.deadline,
		Viewers:   len(ts.viewers),
		State:     state,
	}
}
