package fleetapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/fleetservice"
)

type DispatchRequest = fleetservice.DispatchRequest

type DispatchResponse = fleetservice.DispatchResult

type CancelRequest struct {
	SessionID string `json:"sessionId,omitempty"`
}

func AddRoutes(mux *http.ServeMux, fleet fleetservice.Service) {
	h := &fleetHandler{fleet: fleet}

	mux.HandleFunc("GET /fleet", h.list)
	mux.HandleFunc("POST /fleet/dispatch", h.dispatch)
	mux.HandleFunc("GET /fleet/{instanceID}", h.get)
	mux.HandleFunc("DELETE /fleet/{instanceID}", h.stop)
	mux.HandleFunc("POST /fleet/{instanceID}/cancel", h.cancel)
}

type fleetHandler struct {
	fleet fleetservice.Service
}

func (h *fleetHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.fleet.List(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, entries)
}

func (h *fleetHandler) get(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "instanceID", "The unique ID of the instance.")
	status, err := h.fleet.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, agentinstance.ErrNotFound) {
			err = apiframework.NotFound(err.Error())
		}
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (h *fleetHandler) dispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req, err := apiframework.Decode[DispatchRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}

	result, err := h.fleet.Dispatch(ctx, req)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusAccepted, result)
}

func (h *fleetHandler) stop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "instanceID", "The unique ID of the instance.")
	if err := h.fleet.Stop(ctx, id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "deleted")
}

func (h *fleetHandler) cancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "instanceID", "The unique ID of the instance.")

	var req CancelRequest
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		_ = apiframework.Error(w, r, fmt.Errorf("read request body: %w", err), apiframework.UpdateOperation)
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = apiframework.Error(w, r, fmt.Errorf("%w: %w", apiframework.ErrDecodeInvalidJSON, err), apiframework.UpdateOperation)
			return
		}
	}

	if err := h.fleet.Cancel(ctx, id, req.SessionID); err != nil {
		if errors.Is(err, agentinstance.ErrNotFound) {
			err = apiframework.NotFound(err.Error())
		}
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "cancelled")
}
