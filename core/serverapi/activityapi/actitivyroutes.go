package activityapi

import (
	"net/http"
	"strconv"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/activityservice"
)

func AddActivityRoutes(mux *http.ServeMux, _ *serverops.Config, activityService activityservice.Service) {
	s := &activityAPI{service: activityService}
	mux.HandleFunc("GET /activity/logs", s.list)
	mux.HandleFunc("GET /activity/requests", s.requests)
	mux.HandleFunc("GET /activity/requests/{id}", s.requestByID)
}

type activityAPI struct {
	service activityservice.Service
}

func (s *activityAPI) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse ?limit=N from query (default: 100)
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logs, err := s.service.GetLogs(ctx, limit)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, logs)
}

func (s *activityAPI) requests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	requests, err := s.service.GetRequests(ctx, limit)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, requests)
}

func (s *activityAPI) requestByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	reqID := r.PathValue("id")
	events, err := s.service.GetRequest(ctx, reqID)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, events)
}
