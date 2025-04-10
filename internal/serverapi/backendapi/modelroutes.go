package backendapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/internal/services/modelservice"
)

func AddModelRoutes(mux *http.ServeMux, _ *serverops.Config, modelService *modelservice.Service) {
	m := &modelManager{service: modelService}

	mux.HandleFunc("POST /models", m.append)
	mux.HandleFunc("GET /models", m.list)
	mux.HandleFunc("DELETE /models/{model}", m.delete)
}

type modelManager struct {
	service *modelservice.Service
}

func (m *modelManager) append(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	model, err := serverops.Decode[store.Model](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	model.ID = uuid.NewString()
	if err := m.service.Append(ctx, &model); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, model)
}

func (m *modelManager) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	models, err := m.service.List(ctx)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, models)
}

func (m *modelManager) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	modelName := r.PathValue("model")
	if modelName == "" {
		serverops.Error(w, r, serverops.ErrBadPathValue("model name required"), serverops.DeleteOperation)
		return
	}
	if err := m.service.Delete(ctx, modelName); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
