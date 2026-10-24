package shellenvapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/apiframework/middleware"
	"github.com/contenox/runtime/runtime/shellenvservice"
)

type shellEnvResponse struct {
	Vars map[string]string `json:"vars"`
}

type shellEnvRequest struct {
	Vars map[string]string `json:"vars"`
}

func AddShellEnvRoutes(mux *http.ServeMux, svc shellenvservice.Service, auth middleware.AuthZReader) {
	h := &handler{svc: svc, auth: auth}
	mux.HandleFunc("GET /shell-env", h.get)
	mux.HandleFunc("PUT /shell-env", h.put)
}

type handler struct {
	svc  shellenvservice.Service
	auth middleware.AuthZReader
}

func (h *handler) authorize(r *http.Request) error {
	if h.auth == nil {
		return nil
	}
	_, err := h.auth.GetIdentity(r.Context())
	return err
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	if err := h.authorize(r); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.AuthorizeOperation)
		return
	}
	vars, err := h.svc.Get(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, shellEnvResponse{Vars: vars})
}

func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	if err := h.authorize(r); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.AuthorizeOperation)
		return
	}
	body, err := apiframework.Decode[shellEnvRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if err := h.svc.Set(r.Context(), body.Vars); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	vars, err := h.svc.Get(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, shellEnvResponse{Vars: vars})
}
