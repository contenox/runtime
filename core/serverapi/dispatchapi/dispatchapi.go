package dispatchapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/dispatchservice"
)

func AddDispatchRoutes(mux *http.ServeMux, _ *serverops.Config, dispatchService dispatchservice.Service) {
	h := &dispatchHandler{service: dispatchService}

	mux.HandleFunc("POST /jobs", h.createJob)

	// Job leasing endpoints
	mux.HandleFunc("POST /leases", h.assignJob)
	mux.HandleFunc("PATCH /jobs/{id}/done", h.markDone)
	mux.HandleFunc("PATCH /jobs/{id}/failed", h.markFailed)

	// Job listing endpoints
	mux.HandleFunc("GET /jobs/pending", h.listPending)
	mux.HandleFunc("GET /jobs/in-progress", h.listInProgress)
}

type dispatchHandler struct {
	service dispatchservice.Service
}

type AssignRequest struct {
	LeaserID      string   `json:"leaserId"`
	LeaseDuration string   `json:"leaseDuration"`
	JobTypes      []string `json:"jobTypes"`
}

func (h *dispatchHandler) createJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req, err := serverops.Decode[dispatchservice.CreateJobRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	if err := h.service.CreateJob(ctx, &req); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *dispatchHandler) assignJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req, err := serverops.Decode[AssignRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	var duration time.Duration
	if len(req.LeaseDuration) > 0 {
		duration, err = time.ParseDuration(req.LeaseDuration)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}
	}

	leasedJob, err := h.service.AssignPendingJob(ctx, req.LeaserID, &duration, req.JobTypes...)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, leasedJob)
}

type JobUpdateRequest struct {
	LeaserID string `json:"leaserId"`
}

func (h *dispatchHandler) markDone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := r.PathValue("id")
	if jobID == "" {
		serverops.Error(w, r, fmt.Errorf("id parameter required: %w", serverops.ErrBadPathValue), serverops.UpdateOperation)
		return
	}

	req, err := serverops.Decode[JobUpdateRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	if err := h.service.MarkJobAsDone(ctx, jobID, req.LeaserID); err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *dispatchHandler) markFailed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobID := r.PathValue("id")
	if jobID == "" {
		serverops.Error(w, r, fmt.Errorf("id parameter required: %w", serverops.ErrBadPathValue), serverops.UpdateOperation)
		return
	}

	req, err := serverops.Decode[JobUpdateRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	if err := h.service.MarkJobAsFailed(ctx, jobID, req.LeaserID); err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, "job marked as failed")
}

func (h *dispatchHandler) listPending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cursor, err := parseTimeParam(r, "cursor")
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "1000"
	}

	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	jobs, err := h.service.PendingJobs(ctx, cursor, limitInt)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, jobs)
}

func (h *dispatchHandler) listInProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cursor, err := parseTimeParam(r, "cursor")
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "1000"
	}

	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	jobs, err := h.service.InProgressJobs(ctx, cursor, limitInt)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, jobs)
}

func parseTimeParam(r *http.Request, name string) (*time.Time, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return nil, nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter: %w", name, serverops.ErrInvalidParameterValue)
	}
	return &t, nil
}
