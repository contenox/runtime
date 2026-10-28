package agentregistryapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func AddAgentRegistryRoutes(mux *http.ServeMux, svc agentregistryservice.Service) {
	h := &agentHandler{svc: svc}

	mux.HandleFunc("GET /agents", h.list)
	mux.HandleFunc("POST /agents", h.create)
	mux.HandleFunc("GET /agents/by-name/{name}", h.getByName)
	mux.HandleFunc("GET /agents/{id}", h.get)
	mux.HandleFunc("PUT /agents/{id}", h.update)
	mux.HandleFunc("DELETE /agents/{id}", h.delete)
}

type agentHandler struct {
	svc agentregistryservice.Service
}

func (h *agentHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cursor, limit, err := apiframework.ListParams(r, 100)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	items, err := h.svc.List(ctx, cursor, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, items)
}

func (h *agentHandler) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the agent.")
	agent, err := h.svc.Get(ctx, id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, agent)
}

func (h *agentHandler) getByName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := apiframework.GetPathParam(r, "name", "The unique name of the agent.")
	agent, err := h.svc.GetByName(ctx, name)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, agent)
}

func (h *agentHandler) create(w http.ResponseWriter, r *http.Request) {
	agent, err := apiframework.Decode[runtimetypes.Agent](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	if err := h.svc.Create(r.Context(), &agent); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, agent)
}

func (h *agentHandler) update(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "The unique ID of the agent.")
	agent, err := apiframework.Decode[runtimetypes.Agent](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	agent.ID = id
	if err := h.svc.Update(r.Context(), &agent); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, agent)
}

func (h *agentHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "The unique ID of the agent.")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "agent deleted")
}
