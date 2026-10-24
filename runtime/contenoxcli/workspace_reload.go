package contenoxcli

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/project"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspacegrants"
)

type workspaceRootReloader struct {
	factory *vfs.Factory
	base    []string
	store   runtimetypes.Store
}

func newWorkspaceRootReloader(factory *vfs.Factory, base []string, store runtimetypes.Store) *workspaceRootReloader {
	return &workspaceRootReloader{factory: factory, base: base, store: store}
}

func (r *workspaceRootReloader) effectiveRoots(ctx context.Context) []string {
	roots := make([]string, 0, len(r.base)+4)
	roots = append(roots, r.base...)
	roots = append(roots, workspacegrants.ReadGrants(ctx, r.store)...)
	return roots
}

func (r *workspaceRootReloader) apply(ctx context.Context) error {
	return r.factory.SetRoots(r.effectiveRoots(ctx))
}

func (r *workspaceRootReloader) mutators(bus libbus.Messenger) *localfileapi.RootsMutators {
	return &localfileapi.RootsMutators{
		Add: func(ctx context.Context, path, name string) error {
			name, nameErr := project.NormalizeName(name)
			if nameErr != nil {
				return fmt.Errorf("%w: %v", workspacegrants.ErrInvalidGrant, nameErr)
			}
			if denied, ok := vfs.IsControlPlanePath(path); ok {
				return fmt.Errorf("%w: %q is inside the runtime's control plane (%s) and can never be a workspace root — the runtime never lets a session reach its own config, database, or policies", workspacegrants.ErrInvalidGrant, path, denied)
			}
			if _, err := workspacegrants.Add(ctx, r.store, path); err != nil {
				return err
			}
			if _, err := project.Register(path, name); err != nil {
				slog.Warn("contenox serve: workspace root granted but project marker could not be written",
					"path", path, "error", err)
			}
			return r.applyAndRing(ctx, bus)
		},
		Remove: func(ctx context.Context, path string) error {
			if _, err := workspacegrants.Remove(ctx, r.store, path); err != nil {
				return err
			}
			return r.applyAndRing(ctx, bus)
		},
		Grants: func(ctx context.Context) []string {
			return workspacegrants.ReadGrants(ctx, r.store)
		},
	}
}

func (r *workspaceRootReloader) applyAndRing(ctx context.Context, bus libbus.Messenger) error {
	if err := r.apply(ctx); err != nil {
		return err
	}
	roots := r.factory.Roots()
	slog.Info("contenox serve: workspace roots changed (authenticated grant verb)",
		"count", len(roots), "roots", roots)
	if err := workspacegrants.PublishChanged(ctx, bus, r.effectiveRoots(ctx)); err != nil {
		slog.Warn("contenox serve: workspace-root grant applied but reload doorbell publish failed",
			"error", err)
	}
	return nil
}

func startWorkspaceRootReloader(ctx context.Context, bus libbus.Messenger, reloader *workspaceRootReloader) (func(), error) {
	ch := make(chan []byte, 8)
	sub, err := bus.Stream(ctx, workspacegrants.RootsChangedSubject, ch)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				if err := reloader.apply(runCtx); err != nil {
					slog.Warn("contenox serve: workspace-root reload failed; keeping current roots", "error", err)
					continue
				}
				roots := reloader.factory.Roots()
				slog.Info("contenox serve: workspace roots reloaded from doorbell",
					"count", len(roots), "roots", roots)
			}
		}
	}()
	return func() {
		cancel()
		_ = sub.Unsubscribe()
		wg.Wait()
	}, nil
}
