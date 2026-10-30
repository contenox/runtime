package envelopeapi

import (
	"net/http"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentdecl"
)

// Envelope is one declared mission envelope as the app shows it.
type Envelope struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AddRoutes serves the declared envelopes. The catalog is the same one the ACP
// session lists; the mission dispatch takes an envelope name and the policy
// source resolves the rendered file behind it.
func AddRoutes(mux *http.ServeMux, home string) {
	h := &handler{home: home}
	mux.HandleFunc("GET /envelopes", h.list)
}

type handler struct {
	home string
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	cfg, err := agentdecl.Shipped()
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	if h.home != "" {
		if root, rootErr := agentdecl.LocalRoot(h.home); rootErr == nil {
			if layered, loadErr := agentdecl.Load(r.Context(), root); loadErr == nil {
				cfg = layered
			}
		}
	}
	names := cfg.EnvelopeNames()
	out := make([]Envelope, 0, len(names))
	for _, name := range names {
		envelope, resolveErr := cfg.ResolveEnvelope(name)
		if resolveErr != nil {
			_ = apiframework.Error(w, r, resolveErr, apiframework.ListOperation)
			return
		}
		out = append(out, Envelope{Name: name, Description: envelope.Description})
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}
