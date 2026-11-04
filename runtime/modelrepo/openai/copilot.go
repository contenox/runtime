package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/runtime/internal/models/copilotauth"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type copilotCatalog struct {
	opts    modelrepo.CatalogOptions
	baseURL string
}

func init() {
	modelrepo.RegisterCatalogProvider(copilotauth.ProviderType, func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		if spec.APIKey != "" {
			return nil, fmt.Errorf("github-copilot uses GitHub device sign-in, not an API key")
		}
		if opts.Authorize == nil {
			return nil, copilotauth.ErrLoginRequired
		}
		base := strings.TrimRight(spec.BaseURL, "/")
		if base == "" {
			base = copilotauth.BaseURL
		}
		return &copilotCatalog{opts: opts, baseURL: base}, nil
	})
}

func (c *copilotCatalog) Type() string { return copilotauth.ProviderType }

type copilotModel struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ModelPickerEnabled bool   `json:"model_picker_enabled"`
	Capabilities       struct {
		Type   string `json:"type"`
		Limits struct {
			MaxContextWindowTokens int `json:"max_context_window_tokens"`
			MaxPromptTokens        int `json:"max_prompt_tokens"`
			MaxOutputTokens        int `json:"max_output_tokens"`
		} `json:"limits"`
		Supports struct {
			ToolCalls bool `json:"tool_calls"`
			Streaming bool `json:"streaming"`
			Vision    bool `json:"vision"`
		} `json:"supports"`
	} `json:"capabilities"`
	Policy *struct {
		State string `json:"state"`
	} `json:"policy"`
}

func (c *copilotCatalog) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	headers, err := c.opts.Authorize(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	req.Header.Set("Accept", "application/json")
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub Copilot model list failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, copilotauth.ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		code, message := modelrepo.ProviderErrorDetail(body)
		return nil, modelrepo.ClassifyProviderError(fmt.Errorf("GitHub Copilot model list returned %d: %s", resp.StatusCode, message), resp.StatusCode, code, message)
	}
	var page struct {
		Data []copilotModel `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("invalid GitHub Copilot model list: %w", err)
	}
	var models []modelrepo.ObservedModel
	for _, m := range copilotChatModels(page.Data) {
		ctxLen := m.Capabilities.Limits.MaxContextWindowTokens
		if ctxLen == 0 {
			ctxLen = m.Capabilities.Limits.MaxPromptTokens
		}
		models = append(models, modelrepo.ObservedModel{
			Name:          m.ID,
			ContextLength: ctxLen,
			CapabilityConfig: modelrepo.CapabilityConfig{
				ContextLength:   ctxLen,
				MaxOutputTokens: m.Capabilities.Limits.MaxOutputTokens,
				CanChat:         true,
				CanPrompt:       true,
				CanStream:       true,
				CanVision:       m.Capabilities.Supports.Vision,
			},
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("GitHub Copilot offers no chat models to this account")
	}
	return models, nil
}

func copilotChatModels(all []copilotModel) []copilotModel {
	seen := map[string]bool{}
	var out []copilotModel
	for _, m := range all {
		if m.ID == "" || seen[m.ID] || m.Capabilities.Type != "chat" || !m.ModelPickerEnabled {
			continue
		}
		if m.Policy != nil && m.Policy.State != "" && m.Policy.State != "enabled" {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	return out
}

func (c *copilotCatalog) ProviderFor(m modelrepo.ObservedModel) modelrepo.Provider {
	base := NewOpenAIProvider("", m.Name, []string{c.baseURL}, m.CapabilityConfig, c.opts.HTTPClient, c.opts.Tracker).(*OpenAIProvider)
	base.id = copilotauth.ProviderType + "-" + m.Name
	base.authorize = c.opts.Authorize
	base.providerName = copilotauth.ProviderType
	base.chatOnly = true
	return base
}
