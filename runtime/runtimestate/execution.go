package runtimestate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (s *State) ExecutionProviders(tracker libtracker.ActivityTracker) ProviderFromRuntimeState {
	return func(ctx context.Context, selectors ...string) ([]modelrepo.Provider, error) {
		tx, commit, release, err := s.dbInstance.WithTransaction(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		store := runtimetypes.New(tx)
		selected := make(map[string]BackendRuntimeState)
		if err := refuseAmbiguousRoute(s.Get(ctx), selectors); err != nil {
			return nil, err
		}
		for id, observed := range s.Get(ctx) {
			if !matchesProvider(&observed.Backend, selectors) || observed.Error != "" {
				continue
			}
			backend, err := store.GetBackend(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("read selected connection %q: %w", observed.Name, err)
			}
			if backend.Type != observed.Backend.Type || backend.BaseURL != observed.Backend.BaseURL {
				return nil, fmt.Errorf("connection %q changed since its models were discovered; discovery repeats on its own, and "+remedy.ConnectionState, backend.Name)
			}
			key, err := loadProviderAPIKey(ctx, store, backend)
			if err != nil {
				return nil, fmt.Errorf("load credentials for connection %q: %w", backend.Name, err)
			}
			observed.Backend = *backend
			observed.SetAPIKey(key)
			selected[id] = observed
		}
		if err := commit(ctx); err != nil {
			return nil, err
		}
		return LocalProviderAdapter(ctx, tracker, selected)(ctx, selectors...)
	}
}

type AmbiguousRouteError struct {
	Reference   string
	Connections []string
}

func (e *AmbiguousRouteError) Error() string {
	return fmt.Sprintf("inference route %q matches multiple connections (%s); name one connection: /role <role> <connection> <model> assigns it, /providers lists them", e.Reference, strings.Join(e.Connections, ", "))
}

func refuseAmbiguousRoute(states map[string]BackendRuntimeState, selectors []string) error {
	if len(selectors) != 1 || matchesEveryProvider(selectors) {
		return nil
	}
	reference := strings.TrimSpace(selectors[0])
	if reference == "" || strings.HasPrefix(reference, "connection:") {
		return nil
	}
	var names []string
	for id, observed := range states {
		if reference == id || MatchesBackend(reference, observed.Backend) {
			names = append(names, observed.Backend.Name)
		}
	}
	if len(names) < 2 {
		return nil
	}
	sort.Strings(names)
	return &AmbiguousRouteError{Reference: reference, Connections: names}
}
