package workspacetriggersapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/contenox/runtime/runtime/workflows"
	"net/http"
	"strconv"
	"strings"
	"time"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/contenox/runtime/runtime/workspacetriggers"
)

type CheckFunc func(ctx context.Context, t workspacetriggers.Trigger) error

type Deps struct {
	Workflows   *workflows.Service
	WorkspaceID string
	Events      workspaceevents.Service
	Store       workspacetriggers.Store
	History     workspacetriggers.History
	Sessions    func(root string) map[string]string
	Active      func(root string) bool
	Check       CheckFunc
}

type handler struct {
	deps Deps
}

type triggerView struct {
	workspacetriggers.Trigger
	Running bool `json:"running"`
}

type triggerInput struct {
	workspacetriggers.Trigger
	Enabled *bool `json:"enabled"`
}

type listResponse struct {
	NextCursor string            `json:"nextCursor,omitempty"`
	Sessions   map[string]string `json:"sessions,omitempty"`
	Triggers   []triggerView     `json:"triggers"`
	Events     []string          `json:"events"`
	Active     bool              `json:"active"`
}

type firingsResponse struct {
	NextCursor string                     `json:"nextCursor,omitempty"`
	Firings    []workspacetriggers.Firing `json:"firings"`
}

func AddRoutes(mux *http.ServeMux, deps Deps) {
	h := &handler{deps: deps}
	if deps.Workflows != nil {
		h.workflowRoutes(mux)
	}
	mux.HandleFunc("GET /workspace/schedules/interval", h.validateInterval)
	for _, route := range []string{"GET /workspace/schedules", "POST /workspace/schedules", "PUT /workspace/schedules/{id}", "DELETE /workspace/schedules/{id}"} {
		mux.HandleFunc(route, h.schedules)
	}
	mux.HandleFunc("GET /workspace/triggers", h.list)
	mux.HandleFunc("GET /workspace/triggers/waiting", func(w http.ResponseWriter, r *http.Request) {
		_, scope, err := h.scope(r)
		var waiting workspacetriggers.Waiting
		if err == nil {
			limit, e := pageLimit(r)
			err = e
			if err == nil {
				waiting, err = h.deps.History.WaitingPage(r.Context(), scope, r.URL.Query().Get("cursor"), limit)
			}
		}
		if err != nil {
			_ = apiframework.Error(w, r, mapError(err), apiframework.ListOperation)
			return
		}
		_ = apiframework.Encode(w, r, http.StatusOK, waiting)
	})
	mux.HandleFunc("GET /workspace/triggers/preview", h.preview)
	mux.HandleFunc("POST /workspace/triggers", h.create)
	mux.HandleFunc("PUT /workspace/triggers/{id}", h.update)
	mux.HandleFunc("DELETE /workspace/triggers/{id}", h.delete)
	mux.HandleFunc("GET /workspace/triggers/firings", h.firings)
}

func pageLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 100, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 200 {
		return 0, apiframework.UnprocessableEntity("limit must be between 1 and 200")
	}
	return n, nil
}

func (h *handler) scope(r *http.Request) (string, string, error) {
	root := apiframework.GetQueryParam(r, "root", "", "Workspace root the triggers belong to: a granted root or a directory under one; empty resolves to the default root.")
	granted, err := h.deps.Events.GrantedRoot(root)
	if err != nil {
		return "", "", apiframework.UnprocessableEntity(err.Error())
	}
	return granted, workspaceevents.Scope(h.deps.WorkspaceID, granted), nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, workspacetriggers.ErrInvalidTrigger):
		return apiframework.UnprocessableEntity(err.Error())
	case errors.Is(err, workspacetriggers.ErrNotFound):
		return apiframework.NotFound(err.Error())
	case errors.Is(err, workspacetriggers.ErrNameTaken):
		return apiframework.Conflict(err.Error())
	}
	return err
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	root, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	limit, err := pageLimit(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	triggers, next, err := h.deps.Store.Page(r.Context(), scope, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	busy, err := h.deps.History.Running(r.Context(), scope, triggers)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	out := listResponse{NextCursor: next, Triggers: make([]triggerView, 0, len(triggers)), Events: workspacetriggers.AllowedEvents(), Active: h.deps.Active(root)}
	if h.deps.Sessions != nil {
		out.Sessions = h.deps.Sessions(root)
	}
	for _, t := range triggers {
		out.Triggers = append(out.Triggers, triggerView{Trigger: t, Running: busy[t.ID]})
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) preview(w http.ResponseWriter, r *http.Request) {
	_, scope, err := h.scope(r)
	var matches []workspacetriggers.Trigger
	next := ""
	if err == nil {
		var limit int
		limit, err = pageLimit(r)
		if err == nil {
			matches, next, err = h.deps.Store.MatchPage(r.Context(), scope, r.URL.Query().Get("type"), r.URL.Query().Get("subject"), r.URL.Query().Get("cursor"), limit)
		}
	}
	if err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, map[string]any{"triggers": matches, "nextCursor": next})
}
func (h *handler) check(ctx context.Context, t *workspacetriggers.Trigger) error {
	if err := t.Normalize(); err != nil {
		return err
	}
	if h.deps.Check == nil {
		return nil
	}
	if err := h.deps.Check(ctx, *t); err != nil {
		return fmt.Errorf("%w: %v", workspacetriggers.ErrInvalidTrigger, err)
	}
	return nil
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	root, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	in, err := apiframework.Decode[triggerInput](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	t := in.Trigger
	t.Enabled = in.Enabled == nil || *in.Enabled
	t.CreatedBy = "human"
	t.Root = root
	if err := h.check(r.Context(), &t); err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.CreateOperation)
		return
	}
	if err := h.deps.Store.Create(r.Context(), scope, &t); err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, triggerView{Trigger: t})
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	_, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	in, err := apiframework.Decode[triggerInput](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	t := in.Trigger
	t.ID = r.PathValue("id")
	current, err := h.deps.Store.Get(r.Context(), scope, t.ID)
	if err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.UpdateOperation)
		return
	}
	t.Root = current.Root
	t.Enabled = current.Enabled
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	if err := h.check(r.Context(), &t); err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.UpdateOperation)
		return
	}
	if err := h.deps.Store.Update(r.Context(), scope, &t); err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, triggerView{Trigger: t})
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	_, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	if err := h.deps.Store.Delete(r.Context(), scope, r.PathValue("id")); err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, apiframework.MessageResponse{Message: "trigger removed"})
}

func (h *handler) firings(w http.ResponseWriter, r *http.Request) {
	_, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(apiframework.GetQueryParam(r, "limit", "", "How many recent firings to return, newest first (1â€“200).")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			_ = apiframework.Error(w, r, apiframework.UnprocessableEntity("limit must be between 1 and 200"), apiframework.ListOperation)
			return
		}
		limit = n
	}
	var nid int64
	if raw := r.URL.Query().Get("nid"); raw != "" {
		nid, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || nid < 1 {
			_ = apiframework.Error(w, r, apiframework.UnprocessableEntity("nid must be positive"), apiframework.ListOperation)
			return
		}
	}
	firings, next, err := h.deps.History.Page(r.Context(), scope, r.URL.Query().Get("cursor"), limit, nid, false)
	if err != nil {
		_ = apiframework.Error(w, r, mapError(err), apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, firingsResponse{Firings: firings, NextCursor: next})
}

func (h *handler) schedules(w http.ResponseWriter, r *http.Request) {
	_, scope, err := h.scope(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	store := workspaceevents.Schedules{DB: h.deps.Store.DB}
	var result any
	status := http.StatusOK
	switch r.Method {
	case http.MethodGet:
		limit, e := pageLimit(r)
		if e != nil {
			err = e
			break
		}
		rows, next, e := store.Page(r.Context(), scope, r.URL.Query().Get("cursor"), limit)
		err = e
		result = rows
		if r.URL.Query().Has("limit") || r.URL.Query().Has("cursor") {
			result = map[string]any{"schedules": rows, "nextCursor": next}
		} else if next != "" {
			w.Header().Set("X-Next-Cursor", next)
		}
	case http.MethodPost:
		var input workspaceevents.Schedule
		input, err = apiframework.Decode[workspaceevents.Schedule](r)
		if err == nil {
			input.CreatedBy = "human"
			result, err = store.Create(r.Context(), scope, input, time.Now())
			status = http.StatusCreated
		}
	case http.MethodPut:
		type update struct {
			Enabled *bool `json:"enabled"`
		}
		var input update
		input, err = apiframework.Decode[update](r)
		if err == nil && input.Enabled == nil {
			err = apiframework.UnprocessableEntity("enabled is required")
		}
		if err == nil {
			err = store.Change(r.Context(), scope, r.PathValue("id"), input.Enabled)
		}
	case http.MethodDelete:
		err = store.Change(r.Context(), scope, r.PathValue("id"), nil)
	}
	if errors.Is(err, workspaceevents.ErrSchedule) {
		err = apiframework.UnprocessableEntity(err.Error())
	}
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, status, result)
}

func (h *handler) validateInterval(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.scope(r); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	interval, err := workspaceevents.ParseScheduleInterval(r.URL.Query().Get("every"))
	if err != nil {
		_ = apiframework.Error(w, r, apiframework.UnprocessableEntity(err.Error()), apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, map[string]time.Time{"next": time.Now().UTC().Add(interval)})
}
