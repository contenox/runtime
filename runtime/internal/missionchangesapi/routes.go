package missionchangesapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/missionchanges"
)

func AddRoutes(mux *http.ServeMux, svc missionchanges.Service) {
	h := &handler{svc: svc}
	mux.HandleFunc("GET /missions/{id}/changes", h.changes)
	mux.HandleFunc("GET /missions/{id}/changes/diff", h.diff)
}

type handler struct {
	svc missionchanges.Service
}

func (h *handler) changes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	res, err := h.svc.Changes(ctx, id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, res)
}

func (h *handler) diff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	path := apiframework.GetQueryParam(r, "path", "", "The changed file path to diff.")
	if path == "" {
		_ = apiframework.Error(w, r, apiframework.InvalidParameterValue("path", "the path query parameter is required"), apiframework.GetOperation)
		return
	}
	res, err := h.svc.Diff(ctx, id, path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, res)
}
