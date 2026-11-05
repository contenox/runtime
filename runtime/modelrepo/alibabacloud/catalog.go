package alibabacloud

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

const defaultBaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"

type catalogProvider struct {
	spec    modelrepo.BackendSpec
	client  *http.Client
	tracker libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider(backendType, func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		client := opts.HTTPClient
		if client == nil {
			client = modelrepo.SharedHTTPClient
		}
		return &catalogProvider{spec: spec, client: client, tracker: opts.Tracker}, nil
	})
}

func (p *catalogProvider) Type() string { return backendType }

func (p *catalogProvider) baseURL() string {
	if base := strings.TrimSpace(p.spec.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultBaseURL
}

type modelCard struct {
	ID string `json:"id"`
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
			fmt.Errorf("Alibaba Cloud Model Studio catalog returned %d: %s", response.StatusCode, message), response.StatusCode, code, message)
	}
	var payload struct {
		Data []modelCard `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Alibaba Cloud Model Studio catalog response: %w", err)
	}
	models := make([]modelrepo.ObservedModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		models = append(models, modelrepo.ObservedModel{
			Name:             id,
			CapabilityConfig: capabilityFor(id),
			Meta:             map[string]string{modelrepo.ObservedDisplayNameMetaKey: id},
		})
	}
	return models, nil
}

var nonChatPrefixes = []string{
	"text-embedding",
	"multimodal-embedding",
	"qwen-tts",
	"qwen3-tts",
	"qwen3-asr",
	"qwen-audio-asr",
	"cosyvoice",
	"sambert",
	"paraformer",
	"qwen-image",
	"wan",
	"gte-rerank",
	"qwen-ocr",
}

var thinkingOnlyModels = map[string]struct{}{
	"qwen3.8-2.4t-a95b":      {},
	"qwen3.7-max-preview":    {},
	"qwen3.7-max-2026-05-17": {},
	"glm-5.3":                {},
	"kimi-k2.7-code":         {},
	"minimax-m2.5":           {},
}

var thinkingOnlyPrefixes = []string{
	"deepseek-r1",
	"qwq-",
	"qvq-",
	"qwen3-max-preview",
}

var hybridThinkingPrefixes = []string{
	"qwen3.",
	"qwen-plus",
	"qwen-flash",
	"deepseek-v",
	"glm-",
	"kimi-",
	"minimax-",
}

func capabilityFor(id string) modelrepo.CapabilityConfig {
	lower := strings.ToLower(id)
	caps := modelrepo.CapabilityConfig{Dialect: dialectName}
	if hasPrefix(lower, nonChatPrefixes) {
		caps.CanEmbed = strings.Contains(lower, "embedding")
		return caps
	}
	caps.CanChat = true
	caps.CanStream = true
	caps.CanVision = strings.Contains(lower, "-vl") || strings.Contains(lower, "omni") || strings.HasPrefix(lower, "qwen3.")
	if thinking := thinkingTypes(lower); thinking != nil {
		caps.CanThink = true
		caps.ThinkingTypes = thinking
	}
	return caps
}

func thinkingTypes(lower string) *modelrepo.ThinkingTypes {
	if _, only := thinkingOnlyModels[lower]; only || hasPrefix(lower, thinkingOnlyPrefixes) {
		return &modelrepo.ThinkingTypes{Enabled: true}
	}
	if hasPrefix(lower, hybridThinkingPrefixes) {
		return &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}
	}
	return nil
}

func hasPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return newProvider(p.spec.APIKey, model.Name, p.baseURL(), model.CapabilityConfig, p.client, p.tracker)
}
