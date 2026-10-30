package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const defaultBaseURL = "https://api.deepseek.com"

const (
	imageModality = "image"
	audioModality = "audio"
)

type catalogProvider struct {
	spec    modelrepo.BackendSpec
	client  *http.Client
	tracker libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("deepseek", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		client := opts.HTTPClient
		if client == nil {
			client = modelrepo.SharedHTTPClient
		}
		return &catalogProvider{spec: spec, client: client, tracker: opts.Tracker}, nil
	})
}

func (p *catalogProvider) Type() string { return "deepseek" }

func (p *catalogProvider) baseURL() string {
	if base := strings.TrimSpace(p.spec.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultBaseURL
}

type modelCard struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	InputModalities []string `json:"input_modalities"`
	Effort          *struct {
		SupportedLevels []string `json:"supported_levels"`
		DefaultLevel    string   `json:"default_level"`
	} `json:"effort"`
}

func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL()+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.spec.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.spec.APIKey)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		code, message := modelrepo.ProviderErrorDetail(body)
		return nil, modelrepo.ClassifyProviderError(
			fmt.Errorf("DeepSeek catalog returned %d: %s", response.StatusCode, message), response.StatusCode, code, message)
	}
	var payload struct {
		Data []modelCard `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode DeepSeek catalog response: %w", err)
	}
	models := make([]modelrepo.ObservedModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, modelrepo.ObservedModel{
			Name:             item.ID,
			ContextLength:    item.ContextWindow,
			CapabilityConfig: capabilityFor(item),
			Meta:             modelFacts(item),
		})
	}
	return models, nil
}

func capabilityFor(item modelCard) modelrepo.CapabilityConfig {
	caps := modelrepo.CapabilityConfig{
		Dialect:         dialectName,
		ContextLength:   item.ContextWindow,
		MaxOutputTokens: item.MaxOutputTokens,
		CanChat:         true,
		CanStream:       true,
		CanVision:       slices.Contains(item.InputModalities, imageModality),
		CanAudio:        slices.Contains(item.InputModalities, audioModality),
	}
	if thinking := thinkingTypes(item); thinking != nil {
		caps.CanThink = true
		caps.ThinkingTypes = thinking
	}
	return caps
}

func thinkingTypes(item modelCard) *modelrepo.ThinkingTypes {
	if item.Effort == nil || len(item.Effort.SupportedLevels) == 0 {
		return nil
	}
	declared := &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}
	for _, level := range item.Effort.SupportedLevels {
		normalized, err := reasoning.Normalize(level)
		if err != nil || normalized == reasoning.Off || normalized == reasoning.Auto {
			continue
		}
		if !slices.Contains(declared.EffortLevels, normalized) {
			declared.EffortLevels = append(declared.EffortLevels, normalized)
		}
	}
	if len(declared.EffortLevels) == 0 {
		return nil
	}
	if err := declared.Validate(); err != nil {
		return nil
	}
	return declared
}

func modelFacts(item modelCard) map[string]string {
	display := strings.TrimSpace(item.Name)
	if display == "" {
		display = item.ID
	}
	return map[string]string{modelrepo.ObservedDisplayNameMetaKey: display}
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return newProvider(p.spec.APIKey, model.Name, p.baseURL(), model.CapabilityConfig, p.client, p.tracker)
}
