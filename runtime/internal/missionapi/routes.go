package missionapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/missionservice"
)

type MissionPatch struct {
	Intent     *string `json:"intent,omitempty"`
	Status     *string `json:"status,omitempty"`
	SessionID  string  `json:"sessionId,omitempty"`
	InstanceID string  `json:"instanceId,omitempty"`
}

func AddRoutes(mux *http.ServeMux, svc missionservice.Service) {
	h := &missionHandler{svc: svc}

	mux.HandleFunc("GET /missions", h.list)
	mux.HandleFunc("POST /missions", h.create)
	mux.HandleFunc("GET /missions/{id}", h.get)
	mux.HandleFunc("PATCH /missions/{id}", h.patch)
	mux.HandleFunc("DELETE /missions/{id}", h.delete)
	mux.HandleFunc("POST /missions/{id}/reports", h.addReport)
	mux.HandleFunc("GET /missions/{id}/reports", h.listReports)
}

type missionHandler struct {
	svc missionservice.Service
}

func (h *missionHandler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := apiframework.Decode[missionservice.Mission](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	if err := h.svc.Create(ctx, &m); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, m)
}

func (h *missionHandler) list(w http.ResponseWriter, r *http.Request) {
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

func (h *missionHandler) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	m, err := h.svc.Get(ctx, id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, m)
}

func (h *missionHandler) patch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	p, err := apiframework.Decode[MissionPatch](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}

	m, err := h.svc.Get(ctx, id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}

	if p.Intent != nil || p.Status != nil {
		if p.Intent != nil {
			m.Intent = *p.Intent
		}
		if p.Status != nil {
			m.Status = missionservice.Status(*p.Status)
		}
		if err := h.svc.Update(ctx, m); err != nil {
			_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
			return
		}
	}

	if p.SessionID != "" || p.InstanceID != "" {
		m, err = h.svc.Bind(ctx, id, p.SessionID, p.InstanceID)
		if err != nil {
			_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
			return
		}
	}

	_ = apiframework.Encode(w, r, http.StatusOK, m)
}

func (h *missionHandler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	if err := h.svc.Delete(ctx, id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "deleted")
}

func (h *missionHandler) addReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")
	rep, err := apiframework.Decode[missionservice.Report](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	if err := h.svc.AddReport(ctx, id, &rep); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, rep)
}

func (h *missionHandler) listReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the mission.")

	limit, err := apiframework.LimitParam(r, 100)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	items, err := h.svc.ListReports(ctx, id, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, items)
}
