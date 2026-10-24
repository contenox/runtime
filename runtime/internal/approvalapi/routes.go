package approvalapi

import (
	"errors"
	"net/http"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/hitlservice"
)

type AnswerRequest struct {
	Approved bool   `json:"approved"`
	Answer   string `json:"answer,omitempty"`
}

func AddRoutes(mux *http.ServeMux, svc hitlservice.Service) {
	h := &approvalHandler{svc: svc}

	mux.HandleFunc("GET /approvals", h.list)
	mux.HandleFunc("POST /approvals/{id}", h.answer)
}

type approvalHandler struct {
	svc hitlservice.Service
}

func (h *approvalHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, err := apiframework.LimitParam(r, 100)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	items, err := h.svc.ListPending(ctx, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, items)
}

func (h *approvalHandler) answer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := apiframework.GetPathParam(r, "id", "The unique ID of the approval.")

	req, err := apiframework.Decode[AnswerRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}

	if strings.TrimSpace(req.Answer) != "" {
		if err := h.svc.Answer(ctx, id, req.Answer); err != nil {
			_ = apiframework.Error(w, r, mapRespondError(err), apiframework.UpdateOperation)
			return
		}
		_ = apiframework.Encode(w, r, http.StatusOK, "answered")
		return
	}

	if err := h.svc.Respond(ctx, id, req.Approved); err != nil {
		_ = apiframework.Error(w, r, mapRespondError(err), apiframework.UpdateOperation)
		return
	}

	result := "denied"
	if req.Approved {
		result = "approved"
	}
	_ = apiframework.Encode(w, r, http.StatusOK, result)
}

func mapRespondError(err error) error {
	switch {
	case errors.Is(err, hitlservice.ErrApprovalNotFound):
		return apiframework.NotFound(err.Error())
	case errors.Is(err, hitlservice.ErrApprovalAlreadyResolved):
		return apiframework.Conflict(err.Error())
	case errors.Is(err, hitlservice.ErrApprovalExpired):
		return apiframework.Conflict(err.Error())
	default:
		return err
	}
}
