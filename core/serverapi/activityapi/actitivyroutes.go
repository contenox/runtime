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
