package runtimestate

import (
	"context"
	"net/http"

	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (s *State) processCodexBackend(ctx context.Context, backend *runtimetypes.Backend, declared []*runtimetypes.Model) {
	auth := modelauth.New(s.dbInstance)
	state := &BackendRuntimeState{ID: backend.ID, Name: backend.Name, Backend: *backend}
	defer s.state.Store(backend.ID, state)
	status, err := auth.Status(ctx, backend.ID)
	if err != nil {
		state.setError(err)
		return
	}
	if status.State == "login_required" {
		state.setError(modelauth.ErrLoginRequired)
		return
	}
	state.authorize = func(ctx context.Context) (http.Header, error) { return auth.Headers(ctx, backend.ID) }
	models, cached := s.loadObservedModelCache(ctx, backend.ID, status.Generation)
	if !cached {
		catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: modelauth.ProviderType, BaseURL: backend.BaseURL}, modelrepo.WithCatalogAuthorizer(state.authorize))
		if err != nil {
			state.setError(err)
			return
		}
		models, err = catalog.ListModels(ctx)
		if err != nil {
			state.setError(err)
			return
		}
		s.storeObservedModelCache(ctx, backend.ID, status.Generation, models, catalog)
	}
	state.Models = observedModelNames(models)
	declaredModels := declaredModelsByName(declared)
	for _, m := range models {
		pull := s.applyDeclaredModel(ctx, backend, declaredModels[m.Name], m)
		state.PulledModels = append(state.PulledModels, pull)
	}
}
