package admission

import (
	"context"
	"time"
)

// YieldService cooperatively suspends compute while retaining the worker permit and resident memory.
type YieldService interface {
	WaitYield(context.Context, Permit) error
	ResumeAtBoundary(context.Context, Permit) error
}

func (t *eventTransaction) priorities() []Priority {
	priorities := []Priority{Autocomplete, Foreground, Background}
	if t.state.CompletionBurst >= t.config.ForegroundBurst {
		priorities = []Priority{Foreground, Autocomplete, Background}
	}
	if t.state.Burst >= t.config.ForegroundBurst {
		priorities = append([]Priority{Background}, priorities[:2]...)
	}
	return priorities
}

func (t *eventTransaction) computeFits(workerID string, active map[string]bool) bool {
	if !t.threadsFit(workerID, active) {
		return false
	}
	allocation, _ := t.computeAllocation(workerID)
	for _, pool := range allocation.Pools {
		if pool.PoolID == "host" || pool.Bytes == 0 {
			continue
		}
		for id := range active {
			other, _ := t.computeAllocation(id)
			if allocationBytes(other, pool.PoolID) > 0 {
				return false
			}
		}
	}
	return true
}

func (t *eventTransaction) requestYields(active map[string]bool, waiting []string) error {
	order := map[Priority]int{}
	for index, priority := range t.priorities() {
		order[priority] = index
	}
	for id, request := range t.state.Requests {
		if request.Permit == nil || !request.Accepted || request.Suspended || request.Request.Priority == Autocomplete || !t.state.Allocations[request.Request.WorkerID].CanYield {
			continue
		}
		shouldYield := false
		for _, queuedID := range waiting {
			queued := t.state.Requests[queuedID]
			if queued.Permit != nil && !queued.Suspended || queued.Request.WorkerID == request.Request.WorkerID || order[queued.Request.Priority] >= order[request.Request.Priority] {
				continue
			}
			if !t.computeFits(queued.Request.WorkerID, active) || t.config.GlobalLimit > 0 && len(active) >= t.config.GlobalLimit {
				shouldYield = true
				break
			}
		}
		if shouldYield != request.YieldRequested {
			request.YieldRequested = shouldYield
			if err := t.append("yield_requested", admissionChange{ID: id, Request: &request}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *eventTransaction) permitRequest(permit Permit) (string, eventRequest, error) {
	if permit.ID == 0 || permit.WorkerID == "" || permit.OwnerEpoch == "" {
		return "", eventRequest{}, ErrInvalidPermit
	}
	for id, request := range t.state.Requests {
		if request.Permit != nil && *request.Permit == permit {
			if request.Cancellation != "" {
				return id, request, storedFailure(request.Cancellation)
			}
			return id, request, nil
		}
	}
	return "", eventRequest{}, ErrInvalidPermit
}

func (s *EventScheduler) waitPermit(ctx context.Context, permit Permit, observe func(*eventTransaction, string, eventRequest) (bool, error)) error {
	wake := make(chan []byte, 1)
	sub, err := s.bus.Stream(ctx, admissionWake, wake)
	if err != nil {
		return err
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			s.report(err)
		}
	}()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		ready := false
		if err := s.transact(ctx, func(t *eventTransaction) error {
			if err := s.dispatch(t); err != nil {
				return err
			}
			id, request, err := t.permitRequest(permit)
			if err != nil {
				return err
			}
			ready, err = observe(t, id, request)
			return err
		}); err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-tick.C:
		}
	}
}

// WaitYield waits for the owner to request a safe native boundary; it never marks running work suspended.
func (s *EventScheduler) WaitYield(ctx context.Context, permit Permit) error {
	return s.waitPermit(ctx, permit, func(_ *eventTransaction, _ string, request eventRequest) (bool, error) {
		return request.YieldRequested || request.Suspended, nil
	})
}

// ResumeAtBoundary acknowledges a stopped native step and waits for compute admission.
// The same permit, sampler, context and resident resources remain owned throughout suspension.
func (s *EventScheduler) ResumeAtBoundary(ctx context.Context, permit Permit) error {
	acknowledged := false
	return s.waitPermit(ctx, permit, func(t *eventTransaction, id string, request eventRequest) (bool, error) {
		if !acknowledged && request.YieldRequested {
			request.Suspended, request.YieldRequested = true, false
			if err := t.append("suspended", admissionChange{ID: id, Request: &request}); err != nil {
				return false, err
			}
			if err := s.dispatch(t); err != nil {
				return false, err
			}
			request = t.state.Requests[id]
		}
		acknowledged = true
		return !request.Suspended, nil
	})
}
