package backendapi

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/downloadservice"
	"github.com/contenox/contenox/core/services/modelservice"
	"github.com/google/uuid"
)

func AddModelRoutes(mux *http.ServeMux, _ *serverops.Config, modelService modelservice.Service, dwService downloadservice.Service) {
	s := &service{service: modelService, dwService: dwService}

	mux.HandleFunc("POST /models", s.append)
	mux.HandleFunc("GET /models", s.list)
	mux.HandleFunc("DELETE /models/{model}", s.delete)
}

type service struct {
	service   modelservice.Service
	dwService downloadservice.Service
}

func (s *service) append(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	model, err := serverops.Decode[store.Model](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	model.ID = uuid.NewString()
	if err := s.service.Append(ctx, &model); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, model)
}

func (s *service) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	models, err := s.service.List(ctx)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, models)
}

func (s *service) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	modelName := url.PathEscape(r.PathValue("model"))
	if modelName == "" {
		serverops.Error(w, r, fmt.Errorf("model name is required: %w", serverops.ErrBadPathValue), serverops.DeleteOperation)
		return
	}
	if err := s.service.Delete(ctx, modelName); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}
	queue := r.URL.Query().Get("purge")
	if queue == "true" {
		if err := s.dwService.RemoveDownloadFromQueue(r.Context(), modelName); err != nil {
			_ = serverops.Error(w, r, err, serverops.DeleteOperation)
			return
		}
		if err := s.dwService.CancelDownloads(r.Context(), modelName); err != nil {
			_ = serverops.Error(w, r, err, serverops.DeleteOperation)
			return
		}
	}

	_ = serverops.Encode(w, r, http.StatusOK, "model removed")
}
