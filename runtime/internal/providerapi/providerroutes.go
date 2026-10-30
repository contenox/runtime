package providerapi

import (
	"errors"
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/providerservice"
)

func AddProviderRoutes(mux *http.ServeMux, providerService providerservice.Service) {
	p := &providerManager{providerService: providerService}

	mux.HandleFunc("POST /providers/{providerType}/configure", p.configure)
	mux.HandleFunc("GET /providers/{providerType}/status", p.status)

	mux.HandleFunc("GET /providers/{providerType}/config", p.get)
	mux.HandleFunc("GET /providers/configs", p.listConfigs)
	mux.HandleFunc("GET /providers/supported", p.supported)
	mux.HandleFunc("POST /providers/{providerType}/login", p.startLogin)
	mux.HandleFunc("GET /providers/{providerType}/login", p.loginStatus)
	mux.HandleFunc("POST /providers/{providerType}/login/cancel", p.cancelLogin)

	mux.HandleFunc("GET /providers/connections", p.listConnections)
	mux.HandleFunc("POST /providers/{providerType}/connections", p.createConnection)
	mux.HandleFunc("PATCH /providers/connections/{id}", p.updateConnection)
	mux.HandleFunc("POST /providers/connections/{id}/check", p.checkConnection)
	mux.HandleFunc("POST /providers/connections/{id}/login", p.startConnectionLogin)
	mux.HandleFunc("GET /providers/connections/{id}/login", p.connectionLoginStatus)
	mux.HandleFunc("POST /providers/connections/{id}/login/cancel", p.cancelConnectionLogin)

	// DELETE /providers/connections/{id} overlaps DELETE /providers/{providerType}/config
	// and .../login (e.g. /providers/connections/login), which net/http refuses to
	// register as separate patterns, so one pattern dispatches all three.
	mux.HandleFunc("DELETE /providers/{first}/{second}", p.deleteDispatch)
	mux.HandleFunc("DELETE /providers/connections/{id}/login", p.logoutConnection)
}

type providerManager struct {
	providerService providerservice.Service
}

type ConfigureRequest struct {
	APIKey       string `json:"apiKey"`
	APIKeyEnv    string `json:"apiKeyEnv"`
	BaseURL      string `json:"baseUrl"`
	DefaultModel string `json:"defaultModel"`
	Upsert       bool   `json:"upsert"`
	SetDefault   *bool  `json:"setDefault"`
}

func (p *providerManager) configure(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to configure.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.CreateOperation)
		return
	}
	req, err := apiframework.Decode[ConfigureRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	setDefault := true
	if req.SetDefault != nil {
		setDefault = *req.SetDefault
	}
	status, err := p.providerService.Configure(r.Context(), providerType, providerservice.ConfigureProviderRequest{
		APIKey:       req.APIKey,
		APIKeyEnv:    req.APIKeyEnv,
		BaseURL:      req.BaseURL,
		DefaultModel: req.DefaultModel,
		Upsert:       req.Upsert,
		SetDefault:   setDefault,
	})
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (p *providerManager) status(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to inspect.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.GetOperation)
		return
	}
	status, err := p.providerService.GetProviderConfig(r.Context(), providerType)
	if errors.Is(err, libdb.ErrNotFound) {
		_ = apiframework.Encode(w, r, http.StatusOK, providerservice.ProviderStatus{
			Provider:     providerType,
			Configured:   false,
			SecretSource: "none",
		})
		return
	}
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (p *providerManager) deleteConfig(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to delete.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.DeleteOperation)
		return
	}
	if err := p.providerService.DeleteProviderConfig(r.Context(), providerType); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "provider config deleted")
}

func (p *providerManager) listConfigs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cursor, limit, err := apiframework.ListParams(r, 100)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	configs, err := p.providerService.ListProviderConfigs(ctx, cursor, limit)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	_ = apiframework.Encode(w, r, http.StatusOK, configs)
}

func (p *providerManager) supported(w http.ResponseWriter, r *http.Request) {
	providers, err := p.providerService.ListSupportedProviders(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, providers)
}

func (p *providerManager) get(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to retrieve.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.GetOperation)
		return
	}

	config, err := p.providerService.GetProviderConfig(r.Context(), providerType)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}

	_ = apiframework.Encode(w, r, http.StatusOK, config)
}

func (p *providerManager) startLogin(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to sign in to.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.CreateOperation)
		return
	}
	start, err := p.providerService.StartLogin(r.Context(), providerType)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, start)
}

func (p *providerManager) loginStatus(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to inspect.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.GetOperation)
		return
	}
	status, err := p.providerService.LoginStatus(r.Context(), providerType)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (p *providerManager) logout(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to sign out of.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.DeleteOperation)
		return
	}
	if err := p.providerService.Logout(r.Context(), providerType); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "signed out")
}

func (p *providerManager) cancelLogin(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type whose sign-in attempt to cancel.")
	if providerType == "" {
		_ = apiframework.Error(w, r, errors.New("providerType is required in path"), apiframework.DeleteOperation)
		return
	}
	status, err := p.providerService.CancelLogin(r.Context(), providerType)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

// CreateConnectionRequest is the body of POST /providers/{providerType}/connections.
// Every field is optional unless the provider needs it (an API key or env
// reference for key-based providers, a base URL for self-hosted ones).
type CreateConnectionRequest struct {
	Name      string `json:"name"`
	APIKey    string `json:"apiKey"`
	APIKeyEnv string `json:"apiKeyEnv"`
	BaseURL   string `json:"baseUrl"`
}

// UpdateConnectionRequest is the body of PATCH /providers/connections/{id}.
// Absent fields are left unchanged.
type UpdateConnectionRequest struct {
	Name        *string `json:"name"`
	BaseURL     *string `json:"baseUrl"`
	APIKey      *string `json:"apiKey"`
	APIKeyEnv   *string `json:"apiKeyEnv"`
	ClearSecret bool    `json:"clearSecret"`
}

func (p *providerManager) listConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := p.providerService.ListConnections(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, connections)
}

func (p *providerManager) createConnection(w http.ResponseWriter, r *http.Request) {
	providerType := apiframework.GetPathParam(r, "providerType", "Provider type to create a connection for.")
	var req CreateConnectionRequest
	if r.ContentLength != 0 {
		decoded, err := apiframework.Decode[CreateConnectionRequest](r)
		if err != nil {
			_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
			return
		}
		req = decoded
	}
	conn, err := p.providerService.CreateConnection(r.Context(), providerType, providerservice.CreateConnectionRequest{
		Name:      req.Name,
		APIKey:    req.APIKey,
		APIKeyEnv: req.APIKeyEnv,
		BaseURL:   req.BaseURL,
	})
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, conn)
}

func (p *providerManager) updateConnection(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	req, err := apiframework.Decode[UpdateConnectionRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	conn, err := p.providerService.UpdateConnection(r.Context(), id, providerservice.UpdateConnectionRequest{
		Name:        req.Name,
		BaseURL:     req.BaseURL,
		APIKey:      req.APIKey,
		APIKeyEnv:   req.APIKeyEnv,
		ClearSecret: req.ClearSecret,
	})
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, conn)
}

// checkConnection runs a discovery cycle for the connection synchronously
// (bounded to 30s) and answers 200 with the connection as it stands afterwards.
func (p *providerManager) checkConnection(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	conn, err := p.providerService.CheckConnection(r.Context(), id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, conn)
}

func (p *providerManager) startConnectionLogin(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	start, err := p.providerService.StartConnectionLogin(r.Context(), id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, start)
}

func (p *providerManager) connectionLoginStatus(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	status, err := p.providerService.ConnectionLoginStatus(r.Context(), id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (p *providerManager) cancelConnectionLogin(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	status, err := p.providerService.CancelConnectionLogin(r.Context(), id)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, status)
}

func (p *providerManager) logoutConnection(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	if err := p.providerService.LogoutConnection(r.Context(), id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "signed out")
}

func (p *providerManager) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id := apiframework.GetPathParam(r, "id", "Connection (backend) ID.")
	if err := p.providerService.DeleteConnection(r.Context(), id); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, "connection deleted")
}

// deleteDispatch serves the three DELETE /providers/{x}/{y} shapes:
// /providers/connections/{id}, /providers/{type}/config and /providers/{type}/login.
func (p *providerManager) deleteDispatch(w http.ResponseWriter, r *http.Request) {
	first, second := r.PathValue("first"), r.PathValue("second")
	if first == "connections" {
		r.SetPathValue("id", second)
		p.deleteConnection(w, r)
		return
	}
	r.SetPathValue("providerType", first)
	switch second {
	case "config":
		p.deleteConfig(w, r)
	case "login":
		p.logout(w, r)
	default:
		http.NotFound(w, r)
	}
}
