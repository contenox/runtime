package vllm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

const (
	healthPath  = "/health"
	modelsPath  = "/v1/models"
	openAPIPath = "/openapi.json"

	chatCompletionsPath = "/v1/chat/completions"
	completionsPath     = "/v1/completions"
	embeddingsPath      = "/v1/embeddings"
)

type catalogProvider struct {
	spec       modelrepo.BackendSpec
	httpClient *http.Client
	tracker    libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("vllm", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		return &catalogProvider{
			spec:       spec,
			httpClient: opts.HTTPClient,
			tracker:    opts.Tracker,
		}, nil
	})
}

func (p *catalogProvider) Type() string {
	return "vllm"
}

type vllmModelCard struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int    `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Root        string `json:"root"`
	Parent      string `json:"parent"`
	MaxModelLen int    `json:"max_model_len"`

	CompletionMode  string `json:"completion_mode"`
	FIMFormat       string `json:"fim_format"`
	CanAutocomplete bool   `json:"autocomplete"`
	NextEditFormat  string `json:"next_edit_format"`
	CanNextEdit     bool   `json:"next_edit"`
}

func (p *catalogProvider) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeBaseURL(p.spec.BaseURL)+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if p.spec.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.spec.APIKey)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func (p *catalogProvider) checkHealth(ctx context.Context) error {
	status, body, err := p.get(ctx, healthPath)
	if err != nil {
		return fmt.Errorf("vLLM health check failed: %w", err)
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("vLLM health check returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *catalogProvider) servedRoutes(ctx context.Context) (map[string]bool, error) {
	status, body, err := p.get(ctx, openAPIPath)
	if err != nil {
		return nil, fmt.Errorf("vLLM route document unavailable: %w", err)
	}
	if status != http.StatusOK {
		return nil, nil
	}
	var document struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode vLLM route document: %w", err)
	}
	routes := make(map[string]bool, len(document.Paths))
	for path := range document.Paths {
		routes[path] = true
	}
	return routes, nil
}

func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	if err := p.checkHealth(ctx); err != nil {
		return nil, err
	}

	routes, err := p.servedRoutes(ctx)
	if err != nil {
		return nil, err
	}

	status, body, err := p.get(ctx, modelsPath)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("vLLM catalog returned %d: %s", status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Data []vllmModelCard `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode vLLM catalog response: %w", err)
	}

	models := make([]modelrepo.ObservedModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		observed := modelrepo.ObservedModel{
			Name:          item.ID,
			ContextLength: item.MaxModelLen,
			CapabilityConfig: modelrepo.CapabilityConfig{
				CompletionMode: item.CompletionMode,
				FIMFormat:      item.FIMFormat, CanAutocomplete: item.CanAutocomplete,
				NextEditFormat: item.NextEditFormat, CanNextEdit: item.CanNextEdit,
				ContextLength: item.MaxModelLen,
			},
		}
		applyServedRoutes(&observed, routes)
		models = append(models, observed)
	}
	return models, nil
}

func applyServedRoutes(model *modelrepo.ObservedModel, routes map[string]bool) {
	if routes == nil {
		model.CanChat = true
		model.CanPrompt = true
		model.CanStream = true
		return
	}
	serves := func(route string) bool {
		for path := range routes {
			if strings.HasSuffix(path, route) {
				return true
			}
		}
		return false
	}
	model.CanChat = serves(chatCompletionsPath)
	model.CanPrompt = model.CanChat || serves(completionsPath)
	model.CanStream = model.CanChat || serves(completionsPath)
	model.CanEmbed = serves(embeddingsPath)
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return NewVLLMProvider(
		model.Name,
		[]string{p.spec.BaseURL},
		p.httpClient,
		model.CapabilityConfig,
		p.spec.APIKey,
		p.tracker,
	)
}
