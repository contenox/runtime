package connectorsapi

import (
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/connectorservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func AddRoutes(mux *http.ServeMux, svc connectorservice.Service, db libdb.DBManager) {
	h := &handler{svc: svc, store: func() runtimetypes.Store { return runtimetypes.New(db.WithoutTransaction()) }}
	mux.HandleFunc("GET /connectors", h.list)
	mux.HandleFunc("POST /connectors/{name}/check", h.check)
	mux.HandleFunc("GET /connectors/hidden-tools", h.hidden)
	mux.HandleFunc("PUT /connectors/{name}/hidden-tools", h.setHidden)
}

type handler struct {
	svc   connectorservice.Service
	store func() runtimetypes.Store
}

type hiddenToolsRequest struct {
	Workspace string   `json:"workspace,omitempty"`
	Tools     []string `json:"tools"`
}

func (h *handler) hidden(w http.ResponseWriter, r *http.Request) {
	_ = apiframework.Encode(w, r, http.StatusOK, connectorservice.ListHidden(r.Context(), h.store()))
}

func (h *handler) setHidden(w http.ResponseWriter, r *http.Request) {
	name := apiframework.GetPathParam(r, "name", "The connector name.")
	req, err := apiframework.Decode[hiddenToolsRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if err := connectorservice.SetHidden(r.Context(), h.store(), req.Workspace, name, req.Tools); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, connectorservice.ListHidden(r.Context(), h.store()))
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context(), connectorservice.Filter{})
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	if items == nil {
		items = []connectorservice.Connector{}
	}
	_ = apiframework.Encode(w, r, http.StatusOK, items)
}

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	name := apiframework.GetPathParam(r, "name", "The connector name.")
	item, err := h.svc.Check(r.Context(), name)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, item)
}
