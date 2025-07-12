package hooks

import (
	"context"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
)

// Transition simply returns a transition value without modifying input
type Transition struct {
	transition string
	tracker    serverops.ActivityTracker
}

func NewTransition(transition string, tracker serverops.ActivityTracker) *Transition {
	if tracker == nil {
		tracker = serverops.NoopTracker{}
	}
	return &Transition{
		transition: transition,
		tracker:    tracker,
	}
}

func (h *Transition) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	_, _, end := h.tracker.Start(ctx, "exec", "transition_hook")
	defer end()

	// Return the input unchanged but with our predefined transition
	return taskengine.StatusSuccess, input, dataType, h.transition, nil
}

func (h *Transition) Supports(ctx context.Context) ([]string, error) {
	return []string{h.transition}, nil
}

var _ taskengine.HookRepo = (*Transition)(nil)
