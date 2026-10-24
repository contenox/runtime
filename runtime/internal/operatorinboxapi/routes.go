package operatorinboxapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/operatorinbox"
)

func AddRoutes(mux *http.ServeMux, svc operatorinbox.Service) {
	h := &inboxHandler{svc: svc}
	mux.HandleFunc("GET /operator-inbox", h.list)
}

type inboxHandler struct {
	svc operatorinbox.Service
}

func (h *inboxHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, err := apiframework.LimitParam(r, 100)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	items, err := h.svc.List(ctx, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, items)
}
