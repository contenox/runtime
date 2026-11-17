package modelregistryapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

func AddRoutes(mux *http.ServeMux, svc modelregistryservice.Service, reg modelregistry.Registry, acquisition *modelacquisition.Service, db libdbexec.DBManager) {
	h := &handler{svc: svc, reg: reg, acquisition: acquisition, db: db}
	mux.HandleFunc("POST /model-registry", h.create)
	mux.HandleFunc("GET /model-registry", h.list)
	mux.HandleFunc("POST /model-registry/download", h.download)
	mux.HandleFunc("GET /model-registry/{id}", h.get)
	mux.HandleFunc("PUT /model-registry/{id}", h.update)
	mux.HandleFunc("DELETE /model-registry/{id}", h.delete)
}

type handler struct {
	svc         modelregistryservice.Service
	reg         modelregistry.Registry
	acquisition *modelacquisition.Service
	db          libdbexec.DBManager
}

type downloadRequest struct {
	Name string `json:"name"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, err := apiframework.Decode[runtimetypes.ModelRegistryEntry](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	e.ID = uuid.NewString()
	if err := h.svc.Create(ctx, &e); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, e)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entries, err := h.reg.List(ctx)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, entries)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique identifier for the model registry entry.")
	if id == "" {
		_ = apiframework.Error(w, r, fmt.Errorf("missing id parameter %w", apiframework.ErrBadPathValue), apiframework.GetOperation)
		return
	}
	e, err := h.svc.Get(ctx, id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, e)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique identifier for the model registry entry.")
	if id == "" {
		_ = apiframework.Error(w, r, fmt.Errorf("missing id parameter %w", apiframework.ErrBadPathValue), apiframework.UpdateOperation)
		return
	}
	e, err := apiframework.Decode[runtimetypes.ModelRegistryEntry](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	e.ID = id
	if err := h.svc.Update(ctx, &e); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, e)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique identifier for the model registry entry.")
	if id == "" {
		_ = apiframework.Error(w, r, fmt.Errorf("missing id parameter %w", apiframework.ErrBadPathValue), apiframework.DeleteOperation)
		return
	}
	if err := h.svc.Delete(ctx, id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "model registry entry removed")
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req, err := apiframework.Decode[downloadRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		_ = apiframework.Error(w, r, fmt.Errorf("%w: name is required", apiframework.ErrUnprocessableEntity), apiframework.CreateOperation)
		return
	}

	result := "already downloaded"
	err = h.acquisition.Pull(ctx, req.Name, func(progress modelacquisition.DownloadProgress) error {
		if progress.Downloaded > 0 {
			result = "downloaded"
		}
		return ctx.Err()
	})
	if err != nil {
		var unknown *modelacquisition.UnknownModelError
		if errors.As(err, &unknown) {
			err = fmt.Errorf("%w: %s", apiframework.ErrNotFound, err)
		}
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	if err := autosetup.SetInitialModel(ctx, h.db, req.Name); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, result)
}
