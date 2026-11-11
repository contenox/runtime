package workspacetriggersapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/contenox/runtime/runtime/workspacetriggers"
)

type CheckFunc func(ctx context.Context, t workspacetriggers.Trigger) error

type Deps struct {
	WorkspaceID string
	Events      workspaceevents.Service
	Store       workspacetriggers.Store
	History     workspacetriggers.History
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
	Triggers []triggerView `json:"triggers"`
	Events   []string      `json:"events"`
	Active   bool          `json:"active"`
}

type firingsResponse struct {
	Firings []workspacetriggers.Firing `json:"firings"`
}

func AddRoutes(mux *http.ServeMux, deps Deps) {
	h := &handler{deps: deps}
	mux.HandleFunc("GET /workspace/triggers", h.list)
	mux.HandleFunc("POST /workspace/triggers", h.create)
	mux.HandleFunc("PUT /workspace/triggers/{id}", h.update)
	mux.HandleFunc("DELETE /workspace/triggers/{id}", h.delete)
	mux.HandleFunc("GET /workspace/triggers/firings", h.firings)
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
	triggers, err := h.deps.Store.List(r.Context(), scope)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	busy, err := h.deps.History.Running(r.Context(), scope)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	out := listResponse{Triggers: make([]triggerView, 0, len(triggers)), Events: workspacetriggers.AllowedEvents(), Active: h.deps.Active(root)}
	for _, t := range triggers {
		out.Triggers = append(out.Triggers, triggerView{Trigger: t, Running: busy[t.ID]})
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
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
	if raw := strings.TrimSpace(apiframework.GetQueryParam(r, "limit", "", "How many recent firings to return, newest first (1–200).")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			_ = apiframework.Error(w, r, apiframework.UnprocessableEntity("limit must be between 1 and 200"), apiframework.ListOperation)
			return
		}
		limit = n
	}
	firings, err := h.deps.History.Firings(r.Context(), scope, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, firingsResponse{Firings: firings})
}
