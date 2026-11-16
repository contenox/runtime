package runtimestate

import (
	"context"
	"net/http"
	"strings"

	"github.com/contenox/runtime/runtime/internal/models/copilotauth"
	"github.com/contenox/runtime/runtime/internal/models/loginauth"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (s *State) processLoginBackend(ctx context.Context, backend *runtimetypes.Backend, declared []*runtimetypes.Model) {
	state := &BackendRuntimeState{ID: backend.ID, Name: backend.Name, Backend: *backend}
	auth, ok := loginauth.For(s.dbInstance, backend.Type)
	if !ok {
		state.setError(modelauth.ErrLoginRequired)
		s.state.Store(backend.ID, state)
		return
	}
	defer s.state.Store(backend.ID, state)
	status, err := auth.Status(ctx, backend.ID)
	if err != nil {
		state.setError(err)
		return
	}
	if status.State == "login_required" {
		if strings.EqualFold(backend.Type, copilotauth.ProviderType) {
			state.setError(copilotauth.ErrLoginRequired)
		} else {
			state.setError(modelauth.ErrLoginRequired)
		}
		return
	}
	state.authorize = func(ctx context.Context) (http.Header, error) { return auth.Headers(ctx, backend.ID) }
	models, cached := s.loadObservedModelCache(ctx, backend.ID, status.Generation)
	if !cached {
		catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: backend.Type, BaseURL: backend.BaseURL}, modelrepo.WithCatalogAuthorizer(state.authorize))
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
