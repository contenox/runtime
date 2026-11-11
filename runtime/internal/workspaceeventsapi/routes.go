package workspaceeventsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

const (
	maxBodyBytes     = 16 << 10
	bucketCapacity   = 120
	bucketRefillRate = 60
	heartbeat        = 15 * time.Second
	safetyDrainEvery = 4
)

func AddRoutes(mux *http.ServeMux, service workspaceevents.Service) {
	addRoutes(mux, service, time.Now)
}

func addRoutes(mux *http.ServeMux, service workspaceevents.Service, now func() time.Time) {
	h := &handler{service: service, buckets: map[string]*bucket{}, now: now}
	mux.HandleFunc("POST /workspace/events", h.post)
	mux.HandleFunc("GET /workspace/events", h.list)
	mux.HandleFunc("GET /workspace/events/stream", h.stream)
}

type handler struct {
	service workspaceevents.Service
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func (h *handler) allow(root string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	b, ok := h.buckets[root]
	if !ok {
		b = &bucket{tokens: bucketCapacity, at: now}
		h.buckets[root] = b
	}
	b.tokens += now.Sub(b.at).Seconds() * bucketRefillRate
	if b.tokens > bucketCapacity {
		b.tokens = bucketCapacity
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type postRequest struct {
	Type    string          `json:"type"`
	Subject string          `json:"subject"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type listResponse struct {
	Events []workspaceevents.Event `json:"events"`
	Cursor int64                   `json:"cursor"`
}

func requestRoot(r *http.Request) string {
	return apiframework.GetQueryParam(r, "root", "", "Workspace root the events belong to: a granted root or a directory under one; empty resolves to the default root.")
}

func asAPIError(err error) error {
	switch {
	case errors.Is(err, workspaceevents.ErrUnknownType),
		errors.Is(err, workspaceevents.ErrSourceMismatch),
		errors.Is(err, workspaceevents.ErrInvalidSubject),
		errors.Is(err, workspaceevents.ErrInvalidData),
		errors.Is(err, workspaceevents.ErrDataTooLarge),
		errors.Is(err, workspaceevents.ErrRootNotGranted):
		return apiframework.UnprocessableEntity(err.Error())
	}
	return err
}

func writeRateLimited(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": "too many workspace events; slow down",
		"type":    "rate_limit_error",
		"code":    "rate_limited",
	}})
}

func (h *handler) post(w http.ResponseWriter, r *http.Request) {
	root, err := h.service.GrantedRoot(requestRoot(r))
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.CreateOperation)
		return
	}
	if !h.allow(root) {
		writeRateLimited(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	req, err := apiframework.Decode[postRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	event, err := h.service.Append(r.Context(), root, workspaceevents.Input{
		Type:    req.Type,
		Source:  workspaceevents.SourceEditor,
		Subject: req.Subject,
		Data:    req.Data,
	})
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, event)
}

func parseCursor(raw string) (int64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false, apiframework.UnprocessableEntity("since must be a non-negative event number")
	}
	return n, true, nil
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	root, err := h.service.GrantedRoot(requestRoot(r))
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.ListOperation)
		return
	}
	since, _, err := parseCursor(apiframework.GetQueryParam(r, "since", "", "Return events after this event number; omit to start at the beginning."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	limit := 0
	if raw := apiframework.GetQueryParam(r, "limit", "", "Largest number of events to return; at most 1000."); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n <= 0 {
			_ = apiframework.Error(w, r, apiframework.UnprocessableEntity("limit must be a positive number"), apiframework.ListOperation)
			return
		}
		limit = n
	}
	events, err := h.service.Since(r.Context(), root, since, limit)
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.ListOperation)
		return
	}
	cursor := since
	if len(events) > 0 {
		cursor = events[len(events)-1].NID
	}
	_ = apiframework.Encode(w, r, http.StatusOK, listResponse{Events: events, Cursor: cursor})
}

func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	root, err := h.service.GrantedRoot(requestRoot(r))
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.GetOperation)
		return
	}
	since, given, err := parseCursor(apiframework.GetQueryParam(r, "since", "", "Replay events after this event number, then follow live; omit to follow from now."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	if !given {
		since, given, err = parseCursor(r.Header.Get("Last-Event-ID"))
		if err != nil {
			_ = apiframework.Error(w, r, err, apiframework.GetOperation)
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = apiframework.Error(w, r, fmt.Errorf("streaming unsupported"), apiframework.ServerOperation)
		return
	}

	ctx := r.Context()
	wake := make(chan []byte, 64)
	sub, err := h.service.Subscribe(ctx, root, wake)
	if err != nil {
		_ = apiframework.Error(w, r, asAPIError(err), apiframework.GetOperation)
		return
	}
	defer sub.Unsubscribe()

	if !given {
		since, err = h.service.Latest(ctx, root)
		if err != nil {
			_ = apiframework.Error(w, r, asAPIError(err), apiframework.GetOperation)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if _, err := fmt.Fprintf(w, ": connected cursor=%d\n\n", since); err != nil {
		return
	}
	flusher.Flush()

	drain := func() bool {
		for {
			events, err := h.service.Since(ctx, root, since, 0)
			if err != nil {
				return ctx.Err() == nil
			}
			if len(events) == 0 {
				return true
			}
			for _, event := range events {
				payload, err := json.Marshal(event)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.NID, payload); err != nil {
					return false
				}
				since = event.NID
			}
			flusher.Flush()
		}
	}

	if !drain() {
		return
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	pings := 0
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-wake:
			if !open || !discardQueued(wake) {
				return
			}
			pings = 0
			if !drain() {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
			pings++
			if pings < safetyDrainEvery {
				continue
			}
			pings = 0
			if !drain() {
				return
			}
		}
	}
}

func discardQueued(wake <-chan []byte) bool {
	for {
		select {
		case _, open := <-wake:
			if !open {
				return false
			}
		default:
			return true
		}
	}
}
