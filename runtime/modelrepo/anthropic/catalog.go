package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

type catalogProvider struct {
	spec       modelrepo.BackendSpec
	httpClient *http.Client
	tracker    libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("anthropic", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		client := opts.HTTPClient
		if client == nil {
			client = modelrepo.SharedHTTPClient
		}
		return &catalogProvider{spec: spec, httpClient: client, tracker: opts.Tracker}, nil
	})
}

func (p *catalogProvider) Type() string { return "anthropic" }

func (p *catalogProvider) baseURL() string {
	if base := strings.TrimSpace(p.spec.BaseURL); base != "" {
		return base
	}
	return defaultBaseURL
}

func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	return p.listModels(ctx, "/v1/models")
}

type modelListResponse struct {
	Data    []modelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	LastID  string      `json:"last_id"`
}

type modelInfo struct {
	ID             string             `json:"id"`
	DisplayName    string             `json:"display_name"`
	Lifecycle      string             `json:"lifecycle"`
	CreatedAt      string             `json:"created_at"`
	MaxInputTokens int                `json:"max_input_tokens"`
	MaxTokens      int                `json:"max_tokens"`
	Capabilities   *modelCapabilities `json:"capabilities"`
}

type modelCapabilities struct {
	Thinking      thinkingCapability `json:"thinking"`
	Effort        effortCapability   `json:"effort"`
	ImageInput    capabilitySupport  `json:"image_input"`
	StructuredOut capabilitySupport  `json:"structured_outputs"`
	ContextManage capabilitySupport  `json:"context_management"`
}

type thinkingCapability struct {
	Supported bool          `json:"supported"`
	Types     thinkingTypes `json:"types"`
}

type thinkingTypes struct {
	Adaptive capabilitySupport `json:"adaptive"`
	Enabled  capabilitySupport `json:"enabled"`
	Disabled capabilitySupport `json:"disabled"`
}

type effortCapability struct {
	Supported bool              `json:"supported"`
	Low       capabilitySupport `json:"low"`
	Medium    capabilitySupport `json:"medium"`
	High      capabilitySupport `json:"high"`
	XHigh     capabilitySupport `json:"xhigh"`
	Max       capabilitySupport `json:"max"`
}

type capabilitySupport struct {
	Supported bool `json:"supported"`
}

func (p *catalogProvider) listModels(ctx context.Context, path string) ([]modelrepo.ObservedModel, error) {
	var all []modelrepo.ObservedModel
	afterID := ""
	for {
		url := strings.TrimRight(p.baseURL(), "/") + path
		if afterID != "" {
			url += "?after_id=" + afterID
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if p.spec.APIKey != "" {
			req.Header.Set("x-api-key", p.spec.APIKey)
		}
		req.Header.Set("anthropic-version", anthropicAPIVersion)

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("anthropic catalog %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var payload modelListResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode anthropic catalog response: %w", err)
		}
		for _, item := range payload.Data {
			modifiedAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
			thinking := anthropicThinkingTypes(item.Capabilities)
			all = append(all, modelrepo.ObservedModel{
				Name:          item.ID,
				ContextLength: item.MaxInputTokens,
				ModifiedAt:    modifiedAt,
				CapabilityConfig: modelrepo.CapabilityConfig{
					ContextLength:   item.MaxInputTokens,
					MaxOutputTokens: item.MaxTokens,
					CanChat:         true,
					CanStream:       true,
					CanPrompt:       true,
					CanThink:        thinking != nil,
					CanVision:       anthropicCapabilitiesCanVision(item.Capabilities),
					ThinkingTypes:   thinking,
				},
				Meta: anthropicModelMeta(item),
			})
		}
		if !payload.HasMore {
			break
		}
		afterID = payload.LastID
	}
	return all, nil
}

func anthropicThinkingTypes(caps *modelCapabilities) *modelrepo.ThinkingTypes {
	if caps == nil {
		return nil
	}
	types := &modelrepo.ThinkingTypes{
		Adaptive: caps.Thinking.Types.Adaptive.Supported,
		Enabled:  caps.Thinking.Types.Enabled.Supported,
		Disabled: caps.Thinking.Types.Disabled.Supported,
	}
	for _, level := range []struct {
		name      string
		supported bool
	}{
		{reasoning.Low, caps.Effort.Low.Supported},
		{reasoning.Medium, caps.Effort.Medium.Supported},
		{reasoning.High, caps.Effort.High.Supported},
		{reasoning.XHigh, caps.Effort.XHigh.Supported},
		{reasoning.Max, caps.Effort.Max.Supported},
	} {
		if level.supported {
			types.EffortLevels = append(types.EffortLevels, level.name)
		}
	}
	if err := types.Validate(); err != nil {
		return nil
	}
	return types
}

func anthropicCapabilitiesCanVision(caps *modelCapabilities) bool {
	if caps == nil {
		return false
	}
	return caps.ImageInput.Supported
}

func anthropicModelMeta(item modelInfo) map[string]string {
	meta := map[string]string{}
	if strings.TrimSpace(item.DisplayName) != "" && item.DisplayName != item.ID {
		meta["display_name"] = item.DisplayName
	}
	if strings.TrimSpace(item.Lifecycle) != "" {
		meta["lifecycle"] = item.Lifecycle
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return NewAnthropicProvider(p.spec.APIKey, model.Name, []string{p.baseURL()}, model.CapabilityConfig, p.httpClient, p.tracker)
}
