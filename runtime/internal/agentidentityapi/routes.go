package agentidentityapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentidentity"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type State struct {
	Identity agentidentity.Identity `json:"identity"`
	Me       *agentidentity.Person  `json:"me,omitempty"`
	Keys     []agentidentity.Key    `json:"keys"`
}

type GenerateRequest struct {
	Comment string `json:"comment"`
}

type CheckRequest struct {
	Key  string `json:"key"`
	Host string `json:"host"`
}

type CheckResult struct {
	Signing agentidentity.SigningCheck `json:"signing"`
	Host    *agentidentity.HostCheck   `json:"host,omitempty"`
}

type handler struct {
	store runtimetypes.Store
	keys  agentidentity.KeySource
}

func AddRoutes(mux *http.ServeMux, store runtimetypes.Store, keys agentidentity.KeySource) {
	h := &handler{store: store, keys: keys}
	mux.HandleFunc("GET /agent-identity", h.get)
	mux.HandleFunc("PUT /agent-identity", h.put)
	mux.HandleFunc("POST /agent-identity/keys", h.generate)
	mux.HandleFunc("POST /agent-identity/check", h.check)
}

func (h *handler) state(ctx context.Context) State {
	out := State{Identity: agentidentity.Load(ctx, h.store), Keys: h.keys.Discover()}
	if out.Keys == nil {
		out.Keys = []agentidentity.Key{}
	}
	if me, err := agentidentity.DefaultPerson(ctx); err == nil {
		out.Me = &me
	}
	return out
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	_ = apiframework.Encode(w, r, http.StatusOK, h.state(r.Context()))
}

func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	identity, err := apiframework.Decode[agentidentity.Identity](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if err := identity.Validate(); err != nil {
		_ = apiframework.Error(w, r, fmt.Errorf("%w: %s", apiframework.ErrUnprocessableEntity, err.Error()), apiframework.UpdateOperation)
		return
	}
	if err := agentidentity.Save(r.Context(), h.store, identity); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, h.state(r.Context()))
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[GenerateRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if comment == "" {
		comment = "contenox-agent"
	}
	key, err := h.keys.Generate(comment)
	if err != nil {
		_ = apiframework.Error(w, r, fmt.Errorf("%w: %s", apiframework.ErrConflict, err.Error()), apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, key)
}

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[CheckRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ExecuteOperation)
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = agentidentity.Load(r.Context(), h.store).Key
	}
	out := CheckResult{Signing: h.keys.CheckSigning(key)}
	if host := strings.TrimSpace(req.Host); host != "" && out.Signing.OK {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		hostCheck := h.keys.CheckHost(ctx, key, host)
		out.Host = &hostCheck
	}
	_ = apiframework.Encode(w, r, http.StatusOK, out)
}
