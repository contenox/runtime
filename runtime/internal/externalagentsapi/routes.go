package externalagentsapi

import (
	"net/http"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/externalagents"
)

func AddRoutes(mux *http.ServeMux, svc *externalagents.Service) {
	if mux == nil || svc == nil {
		return
	}
	h := &handler{svc: svc}
	mux.HandleFunc("GET /external-agents", h.list)
	mux.HandleFunc("POST /external-agents", h.create)
	mux.HandleFunc("GET /external-agents/{name}", h.get)
	mux.HandleFunc("PUT /external-agents/{name}", h.update)
	mux.HandleFunc("DELETE /external-agents/{name}", h.delete)
	mux.HandleFunc("POST /external-agents/{name}/enable", h.enable)
	mux.HandleFunc("POST /external-agents/{name}/disable", h.disable)
	mux.HandleFunc("POST /external-agents/{name}/check", h.check)
	mux.HandleFunc("POST /external-agents/{name}/login", h.startLogin)
	mux.HandleFunc("GET /external-agents/{name}/login", h.loginStatus)
	mux.HandleFunc("POST /external-agents/{name}/login/code", h.loginCode)
	mux.HandleFunc("DELETE /external-agents/{name}/login", h.cancelLogin)
	mux.HandleFunc("GET /external-agent-presets", h.presets)
	mux.HandleFunc("POST /external-agent-presets/{id}/install", h.install)
}

type handler struct {
	svc *externalagents.Service
}

func name(r *http.Request) string {
	return apiframework.GetPathParam(r, "name", "The external agent's name.")
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Get(r.Context(), name(r))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	in, err := apiframework.Decode[externalagents.Input](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	out, err := h.svc.Create(r.Context(), in)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, out)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	in, err := apiframework.Decode[externalagents.Input](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	out, err := h.svc.Update(r.Context(), name(r), in)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), name(r)); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, map[string]string{"deleted": name(r)})
}

func (h *handler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	out, err := h.svc.SetEnabled(r.Context(), name(r), enabled)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) enable(w http.ResponseWriter, r *http.Request)  { h.setEnabled(w, r, true) }
func (h *handler) disable(w http.ResponseWriter, r *http.Request) { h.setEnabled(w, r, false) }

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Check(r.Context(), name(r))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

type loginRequest struct {
	MethodID string `json:"methodId"`
}

type codeRequest struct {
	Code string `json:"code"`
}

func (h *handler) startLogin(w http.ResponseWriter, r *http.Request) {
	in, err := apiframework.Decode[loginRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	out, err := h.svc.StartLogin(r.Context(), name(r), in.MethodID)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) loginStatus(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.LoginStatus(name(r))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) loginCode(w http.ResponseWriter, r *http.Request) {
	in, err := apiframework.Decode[codeRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	out, err := h.svc.SubmitLoginCode(r.Context(), name(r), in.Code)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) cancelLogin(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.CancelLogin(name(r))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) presets(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Presets(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}

func (h *handler) install(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "The preset to install.")
	out, err := h.svc.InstallPreset(r.Context(), id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusAccepted, out)
}
