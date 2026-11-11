package eventlog

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
)

const DefaultDrainTimeout = 5 * time.Second

type Trigger interface {
	HandleEvent(ctx context.Context, events ...*runtimetypes.Event)
}

type NoopTrigger struct{}

func (NoopTrigger) HandleEvent(context.Context, ...*runtimetypes.Event) {}

var _ Trigger = NoopTrigger{}

type TriggerHolder struct {
	t    atomic.Value
	gate firingGate
}

func NewTriggerHolder() *TriggerHolder { return &TriggerHolder{} }

func (h *TriggerHolder) Set(t Trigger) {
	if t != nil {
		h.t.Store(&t)
	}
}

func (h *TriggerHolder) HandleEvent(ctx context.Context, events ...*runtimetypes.Event) {
	if v, ok := h.t.Load().(*Trigger); ok {
		(*v).HandleEvent(ctx, events...)
	}
}

func (h *TriggerHolder) Drain(timeout time.Duration) bool {
	drained := h.gate.outstanding()
	if drained == nil {
		return true
	}
	if timeout <= 0 {
		<-drained
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-drained:
		return true
	case <-timer.C:
		return false
	}
}

var _ Trigger = (*TriggerHolder)(nil)

type firingGate struct {
	mu sync.Mutex
	n  int

	drained chan struct{}
}

func (g *firingGate) begin() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n == 0 {
		g.drained = make(chan struct{})
	}
	g.n++
}

func (g *firingGate) end() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n--
	if g.n > 0 {
		return
	}
	g.n = 0
	if g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

func (g *firingGate) outstanding() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.drained
}

func fireTrigger(ctx context.Context, trigger Trigger, events ...*runtimetypes.Event) {
	if trigger == nil || len(events) == 0 {
		return
	}
	fireCtx := context.WithoutCancel(ctx)
	if h, ok := trigger.(*TriggerHolder); ok {
		h.gate.begin()
		go func() {
			defer h.gate.end()
			h.HandleEvent(fireCtx, events...)
		}()
		return
	}
	go trigger.HandleEvent(fireCtx, events...)
}
