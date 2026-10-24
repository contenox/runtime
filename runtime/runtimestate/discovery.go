package runtimestate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const discoveryConcurrency = 4

type backendJob struct {
	backend *runtimetypes.Backend
	models  []*runtimetypes.Model
}

type backendRun struct {
	done chan struct{}
	err  error
	job  backendJob
}

func matchesEveryProvider(providers []string) bool {
	for _, provider := range providers {
		if modelrepo.CanonicalBackendType(provider) != "" {
			return false
		}
	}
	return true
}

func matchesProvider(backend *runtimetypes.Backend, providers []string) bool {
	if matchesEveryProvider(providers) {
		return true
	}
	for _, provider := range providers {
		if strings.TrimSpace(provider) == backend.ID {
			return true
		}
		if MatchesBackend(provider, *backend) {
			return true
		}
	}
	return false
}

func (s *State) cleanupProviderBackends(ctx context.Context, currentIDs map[string]struct{}, providers []string) error {
	var result error
	s.state.Range(func(key, value any) bool {
		backend := value.(*BackendRuntimeState)
		if _, exists := currentIDs[backend.ID]; !exists && matchesProvider(&backend.Backend, providers) {
			current, err := s.currentBackendJob(ctx, backend.ID)
			if errors.Is(err, libdbexec.ErrNotFound) || (err == nil && !matchesProvider(current.backend, providers)) {
				s.state.CompareAndDelete(key, value)
			} else if err != nil {
				result = errors.Join(result, err)
			}
		}
		return true
	})
	return result
}

func (s *State) runBackendJobs(ctx context.Context, jobs []backendJob) error {
	queue := make(chan backendJob)
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var result error
	for range min(discoveryConcurrency, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range queue {
				if err := s.reconcileBackend(ctx, job); err != nil {
					errMu.Lock()
					result = errors.Join(result, err)
					errMu.Unlock()
				}
			}
		}()
	}
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			close(queue)
			workers.Wait()
			return errors.Join(result, ctx.Err())
		}
	}
	close(queue)
	workers.Wait()
	return result
}

func (s *State) reconcileBackend(ctx context.Context, job backendJob) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.backendMu.Lock()
		if running := s.backendRuns[job.backend.ID]; running != nil {
			s.backendMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-running.done:
				if (running.err != nil || !reflect.DeepEqual(running.job, job)) && ctx.Err() == nil {
					continue
				}
				return running.err
			}
		}
		run := &backendRun{done: make(chan struct{}), job: job}
		if s.backendRuns == nil {
			s.backendRuns = make(map[string]*backendRun)
		}
		s.backendRuns[job.backend.ID] = run
		s.backendMu.Unlock()
		current, err := s.currentBackendJob(ctx, job.backend.ID)
		if err == nil {
			s.processBackend(ctx, current.backend, current.models)
			err = ctx.Err()
		} else if errors.Is(err, libdbexec.ErrNotFound) {
			s.state.Delete(job.backend.ID)
			err = nil
		}
		s.backendMu.Lock()
		run.err = err
		delete(s.backendRuns, job.backend.ID)
		close(run.done)
		s.backendMu.Unlock()
		return run.err
	}
}

func (s *State) currentBackendJob(ctx context.Context, id string) (backendJob, error) {
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return backendJob{}, err
	}
	if !s.withgroups {
		models, err := store.ListAllModels(ctx)
		return backendJob{backend, models}, err
	}
	groups, err := store.ListAllAffinityGroups(ctx)
	if err != nil {
		return backendJob{}, err
	}
	models := make(map[string]*runtimetypes.Model)
	member := false
	for _, group := range groups {
		backends, err := store.ListBackendsForAffinityGroup(ctx, group.ID)
		if err != nil {
			return backendJob{}, err
		}
		for _, candidate := range backends {
			if candidate.ID != id {
				continue
			}
			member = true
			declared, err := store.ListModelsForAffinityGroup(ctx, group.ID)
			if err != nil {
				return backendJob{}, err
			}
			for _, model := range declared {
				models[model.Model] = model
			}
		}
	}
	if !member {
		return backendJob{}, libdbexec.ErrNotFound
	}
	job := backendJob{backend: backend}
	for _, model := range models {
		job.models = append(job.models, model)
	}
	return job, nil
}
