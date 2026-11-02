package envelopeapi

import (
	"github.com/contenox/runtime/runtime/project"
	"net/http"
	"strings"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/hitlservice"
)

// Envelope is one declared mission envelope as the app shows it.
type Envelope struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// AddRoutes serves the declared envelopes. The catalog is the same one the ACP
// session lists; the mission dispatch takes an envelope name and the policy
// source resolves the rendered file behind it.
func AddRoutes(mux *http.ServeMux, home string, policies hitlservice.PolicySource) {
	h := &handler{home: home, policies: policies}
	mux.HandleFunc("GET /envelopes", h.list)
}

type handler struct {
	home     string
	policies hitlservice.PolicySource
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.policies.(hitlservice.PolicyLister); ok {
		choices := hitlservice.ChoicesFor(h.policies, workspaceIDFromRequest(r))
		out := make([]Envelope, 0, len(choices))
		for _, c := range choices {
			out = append(out, Envelope{Name: c.Name, Description: c.Description, Source: c.Source, Scope: c.Scope})
		}
		_ = apiframework.Encode(w, r, http.StatusOK, out)
		return
	}
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

func workspaceIDFromRequest(r *http.Request) string {
	path := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if path == "" {
		return ""
	}
	return project.LayerKeyOf(path)
}
