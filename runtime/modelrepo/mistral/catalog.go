package mistral

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

const defaultBaseURL = "https://api.mistral.ai/v1"

type catalogProvider struct {
	spec    modelrepo.BackendSpec
	client  *http.Client
	tracker libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("mistral", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		client := opts.HTTPClient
		if client == nil {
			client = modelrepo.SharedHTTPClient
		}
		return &catalogProvider{spec: spec, client: client, tracker: opts.Tracker}, nil
	})
}

func (p *catalogProvider) Type() string { return "mistral" }

func (p *catalogProvider) baseURL() string {
	if base := strings.TrimSpace(p.spec.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultBaseURL
}

type modelCard struct {
	ID               string `json:"id"`
	MaxContextLength int    `json:"max_context_length"`
	Capabilities     struct {
		Chat            bool `json:"completion_chat"`
		FIM             bool `json:"completion_fim"`
		FunctionCalling bool `json:"function_calling"`
		Vision          bool `json:"vision"`
		Classification  bool `json:"classification"`
		Moderation      bool `json:"moderation"`
	} `json:"capabilities"`
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
		return nil, modelrepo.ClassifyProviderError(fmt.Errorf("mistral catalog returned %d: %s", response.StatusCode, strings.TrimSpace(string(body))), response.StatusCode, "", strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []modelCard `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Mistral catalog response: %w", err)
	}
	models := make([]modelrepo.ObservedModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, modelrepo.ObservedModel{
			Name:             item.ID,
			ContextLength:    item.MaxContextLength,
			CapabilityConfig: capabilityFor(item),
			Meta:             modelFacts(item),
		})
	}
	return models, nil
}

func capabilityFor(item modelCard) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		Dialect:         dialectName,
		ContextLength:   item.MaxContextLength,
		CanChat:         item.Capabilities.Chat,
		CanStream:       item.Capabilities.Chat,
		CanAutocomplete: item.Capabilities.FIM,
		CompletionMode:  completionMode(item.Capabilities.FIM),
		FIMFormat:       fimFormat(item.Capabilities.FIM),
		CanVision:       item.Capabilities.Vision,
	}
}

func completionMode(supported bool) string {
	if supported {
		return runtimetypes.CompletionModeFIM
	}
	return ""
}

func modelFacts(item modelCard) map[string]string {
	facts := map[string]string{
		modelrepo.ObservedFunctionCallingMetaKey: strconv.FormatBool(item.Capabilities.FunctionCalling),
		modelrepo.ObservedDisplayNameMetaKey:     item.ID,
	}
	return facts
}

func fimFormat(supported bool) string {
	if supported {
		return "mistral-fim"
	}
	return ""
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return newProvider(p.spec.APIKey, model.Name, p.baseURL(), model.CapabilityConfig, p.client, p.tracker)
}
