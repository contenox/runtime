package runtimestate

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type discoveryLifecycle struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	running   bool
	stopped   bool
	cycleDone chan struct{}
	pending   map[string]bool
	workers   sync.WaitGroup
}

func (s *State) StartDiscovery(ctx context.Context) (stop func(), err error) {
	if s.discovery != nil {
		return nil, errors.New("backend discovery already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	d := &discoveryLifecycle{ctx: ctx, cancel: cancel, pending: map[string]bool{}}
	s.discovery = d
	if err := d.refresh(ctx, s); err != nil {
		d.stop()
		return nil, err
	}
	return d.stop, nil
}

func (s *State) discoveryTracker() libtracker.ActivityTracker {
	if s.tracker == nil {
		return libtracker.NoopTracker{}
	}
	return s.tracker
}

func (d *discoveryLifecycle) refresh(ctx context.Context, s *State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return context.Canceled
	}
	if d.running || !s.claimReconcile(time.Now()) {
		return nil
	}
	d.running = true
	done := make(chan struct{})
	d.cycleDone = done
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		reportErr, _, end := s.discoveryTracker().Start(d.ctx, "sync", "backend_cycle")
		if err := s.RunBackendCycle(d.ctx); err != nil {
			reportErr(err)
		}
		end()
		d.mu.Lock()
		d.running = false
		d.cycleDone = nil
		d.mu.Unlock()
		close(done)
	}()
	return nil
}

func (d *discoveryLifecycle) refreshProviders(s *State, ids []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return context.Canceled
	}
	scheduled := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || d.pending[id] {
			continue
		}
		d.pending[id] = true
		scheduled = append(scheduled, id)
	}
	if len(scheduled) == 0 {
		return nil
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		reportErr, _, end := s.discoveryTracker().Start(d.ctx, "sync", "provider_cycle", "providers", scheduled)
		if err := s.RefreshProviderModels(d.ctx, scheduled...); err != nil {
			reportErr(err)
		}
		end()
		d.mu.Lock()
		for _, id := range scheduled {
			delete(d.pending, id)
		}
		d.mu.Unlock()
	}()
	return nil
}

func (d *discoveryLifecycle) wait(ctx context.Context) error {
	d.mu.Lock()
	done := d.cycleDone
	d.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *discoveryLifecycle) stop() {
	d.mu.Lock()
	d.stopped = true
	d.cancel()
	d.mu.Unlock()
	d.workers.Wait()
}

func (s *State) ScheduleProviderRefresh(ctx context.Context, providers ...string) error {
	if s.discovery != nil {
		return s.discovery.refreshProviders(s, providers)
	}
	return s.RefreshProviderModels(ctx, providers...)
}

func (s *State) RecheckIfStale(ctx context.Context) error {
	if s.discovery != nil {
		if err := s.discovery.wait(ctx); err != nil {
			return err
		}
	}
	if !s.claimReconcile(time.Now()) {
		return nil
	}
	return s.RunBackendCycle(ctx)
}

func (s *State) scheduleUnobserved(ctx context.Context) error {
	backends, err := runtimetypes.New(s.dbInstance.WithoutTransaction()).ListAllBackends(ctx)
	if err != nil {
		return err
	}
	var ids []string
	for _, backend := range backends {
		value, observed := s.state.Load(backend.ID)
		if !observed {
			ids = append(ids, backend.ID)
			continue
		}
		if current, ok := value.(*BackendRuntimeState); ok && current.Backend.Revision != backend.Revision {
			ids = append(ids, backend.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return s.discovery.refreshProviders(s, ids)
}
