package openai

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

const defaultBaseURL = "https://api.openai.com/v1"

type catalogProvider struct {
	spec       modelrepo.BackendSpec
	httpClient *http.Client
	tracker    libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("openai", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		return &catalogProvider{
			spec:       spec,
			httpClient: opts.HTTPClient,
			tracker:    opts.Tracker,
		}, nil
	})
}

func (p *catalogProvider) Type() string {
	return "openai"
}

func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	type modelItem struct {
		ID              string   `json:"id"`
		InputModalities []string `json:"input_modalities"`
	}
	type modelsPage struct {
		Data []modelItem `json:"data"`
	}

	url := strings.TrimRight(p.baseURL(), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if p.spec.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.spec.APIKey)
	}

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
		code, message := modelrepo.ProviderErrorDetail(body)
		return nil, modelrepo.ClassifyProviderError(
			fmt.Errorf("OpenAI catalog returned %d: %s", resp.StatusCode, message), resp.StatusCode, code, message)
	}

	var page modelsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode OpenAI catalog response: %w", err)
	}
	all := make([]modelrepo.ObservedModel, 0, len(page.Data))
	for _, item := range page.Data {
		observed := inferObservedModel(item.ID)
		applyOpenAIModalities(&observed, item.InputModalities)
		all = append(all, observed)
	}
	return all, nil
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return NewOpenAIProvider(
		p.spec.APIKey,
		model.Name,
		[]string{p.baseURL()},
		model.CapabilityConfig,
		p.httpClient,
		p.tracker,
	)
}

func (p *catalogProvider) baseURL() string {
	base := strings.TrimSpace(p.spec.BaseURL)
	if base == "" {
		return defaultBaseURL
	}
	return base
}

func inferObservedModel(id string) modelrepo.ObservedModel {
	lower := strings.ToLower(id)
	observed := modelrepo.ObservedModel{
		Name:          id,
		ContextLength: 0,
	}
	observed.MaxOutputTokens = openAIMaxOutputTokens(id)

	switch {
	case strings.HasPrefix(lower, "text-embedding-"):
		observed.CanEmbed = true
	case strings.Contains(lower, "-instruct"),
		strings.HasPrefix(lower, "davinci-"),
		strings.HasPrefix(lower, "babbage-"):
		observed.CanPrompt = true
	case strings.HasPrefix(lower, "dall-e-"),
		strings.HasPrefix(lower, "sora-"),
		strings.HasPrefix(lower, "chatgpt-image-"),
		strings.Contains(lower, "-image-") && !strings.HasPrefix(lower, "gpt-image-"):
	case strings.HasPrefix(lower, "gpt-image-"):
	case strings.HasPrefix(lower, "tts-"),
		strings.HasSuffix(lower, "-tts"),
		strings.Contains(lower, "-tts-"),
		strings.HasPrefix(lower, "whisper-"),
		strings.Contains(lower, "-audio-"),
		strings.HasPrefix(lower, "gpt-audio"),
		strings.HasPrefix(lower, "gpt-realtime"),
		strings.Contains(lower, "-realtime-"),
		strings.Contains(lower, "-transcribe"),
		strings.HasPrefix(lower, "omni-"):
	case strings.HasPrefix(lower, "gpt-"),
		strings.HasPrefix(lower, "o1"),
		strings.HasPrefix(lower, "o3"),
		strings.HasPrefix(lower, "o4"):
		observed.CanChat = true
		observed.CanPrompt = true
		observed.CanStream = true
	default:
		observed.CanChat = true
		observed.CanPrompt = true
		observed.CanStream = true
	}

	observed.CanVision = observed.CanChat && openAIModelSupportsVision(id)
	observed.CanThink = observed.CanChat && openAIModelReasons(id)
	if observed.CanThink {
		observed.ThinkingTypes = openAIThinkingTypes(id)
	}

	return observed
}

func applyOpenAIModalities(model *modelrepo.ObservedModel, modalities []string) {
	for _, modality := range modalities {
		switch strings.ToLower(strings.TrimSpace(modality)) {
		case "image":
			model.CanVision = true
		case "audio":
			model.CanAudio = true
		}
	}
}
