package providersapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/contenox/contenox/core/services/providerservice"
)

func AddProviderRoutes(mux *http.ServeMux, providerService providerservice.Service) {
	p := &providerManager{providerService: providerService}

	mux.HandleFunc("POST /providers/openai/configure", p.configure("openai"))
	mux.HandleFunc("POST /providers/gemini/configure", p.configure("gemini"))
	mux.HandleFunc("GET /providers/openai/status", p.status("openai"))
	mux.HandleFunc("GET /providers/gemini/status", p.status("gemini"))
	// mux.HandleFunc("GET /providers", p.listProviders)
}

type providerManager struct {
	providerService providerservice.Service
}

type ConfigureRequest struct {
	APIKey    string `json:"apiKey"`
	ModelName string `json:"modelName,omitempty"`
}

type StatusResponse struct {
	Configured bool      `json:"configured"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Provider   string    `json:"provider"`
}

func (p *providerManager) configure(providerType string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ConfigureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.APIKey == "" {
			http.Error(w, "API key is required", http.StatusBadRequest)
			return
		}

		cfg := &providerservice.ProviderConfig{
			APIKey:    req.APIKey,
			ModelName: req.ModelName,
			Type:      providerType,
		}

		if err := p.providerService.SetProviderConfig(r.Context(), providerType, cfg); err != nil {
			http.Error(w, "Failed to save API key", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "configured"})
	}
}

func (p *providerManager) status(providerType string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := p.providerService.GetProviderConfig(r.Context(), providerType)
		if err != nil {
			json.NewEncoder(w).Encode(StatusResponse{
				Configured: false,
				Provider:   providerType,
			})
			return
		}

		json.NewEncoder(w).Encode(StatusResponse{
			Configured: true,
			Provider:   providerType,
		})
	}
}
