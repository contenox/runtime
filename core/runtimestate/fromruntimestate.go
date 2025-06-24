package runtimestate

import (
	"context"
	"fmt"
	"net/http"

	"github.com/contenox/contenox/libs/libmodelprovider"
)

// ProviderConfig holds configuration for cloud providers
type ProviderConfig struct {
	APIKey    string
	ModelName string
}

// ProviderFromRuntimeState retrieves available model providers
type ProviderFromRuntimeState func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error)

// NewProviderAdapter creates a unified provider adapter that includes:
// 1. Self-hosted providers (Ollama, vLLM) from runtime state
// 2. Cloud providers (Gemini, OpenAI) from configuration
func NewProviderAdapter(
	ctx context.Context,
	runtime map[string]LLMState,
	geminiConfigs []ProviderConfig,
	openaiConfigs []ProviderConfig,
) ProviderFromRuntimeState {
	// Create self-hosted providers from runtime state
	selfHostedProviderFn := ModelProviderAdapter(ctx, runtime)

	// Create cloud providers from configuration
	cloudProviders := createCloudProviders(geminiConfigs, openaiConfigs)

	return func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error) {
		var providers []libmodelprovider.Provider

		// Get self-hosted providers
		if selfHostedProviderFn != nil {
			selfHosted, err := selfHostedProviderFn(ctx, backendTypes...)
			if err != nil {
				return nil, fmt.Errorf("error getting self-hosted providers: %w", err)
			}
			providers = append(providers, selfHosted...)
		}

		// Add cloud providers for requested types
		for _, t := range backendTypes {
			if cloudProvidersForType, ok := cloudProviders[t]; ok {
				providers = append(providers, cloudProvidersForType...)
			}
		}

		return providers, nil
	}
}

// ModelProviderAdapter creates providers for self-hosted backends (Ollama, vLLM)
func ModelProviderAdapter(ctx context.Context, runtime map[string]LLMState) ProviderFromRuntimeState {
	// Create a two-level map: backendType -> modelName -> []baseURLs
	modelsByBackendType := make(map[string]map[string][]string)

	for _, state := range runtime {
		backendType := state.Backend.Type
		baseURL := state.Backend.BaseURL

		if _, ok := modelsByBackendType[backendType]; !ok {
			modelsByBackendType[backendType] = make(map[string][]string)
		}

		for _, model := range state.PulledModels {
			modelName := model.Model
			modelsByBackendType[backendType][modelName] = append(
				modelsByBackendType[backendType][modelName],
				baseURL,
			)
		}
	}

	// Create providers grouped by backend type
	providersByType := make(map[string][]libmodelprovider.Provider)
	for backendType, modelMap := range modelsByBackendType {
		for modelName, baseURLs := range modelMap {
			switch backendType {
			case "ollama":
				providersByType["ollama"] = append(providersByType["ollama"],
					libmodelprovider.NewOllamaModelProvider(modelName, baseURLs, http.DefaultClient))
			case "vllm":
				providersByType["vllm"] = append(providersByType["vllm"],
					libmodelprovider.NewVLLMModelProvider(modelName, baseURLs, http.DefaultClient))
			}
		}
	}

	return func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error) {
		var providers []libmodelprovider.Provider
		for _, backendType := range backendTypes {
			if typeProviders, ok := providersByType[backendType]; ok {
				providers = append(providers, typeProviders...)
			}
		}
		return providers, nil
	}
}

// createCloudProviders creates providers for cloud-based services (Gemini, OpenAI)
func createCloudProviders(
	geminiConfigs []ProviderConfig,
	openaiConfigs []ProviderConfig,
) map[string][]libmodelprovider.Provider {
	cloudProviders := make(map[string][]libmodelprovider.Provider)

	// Create Gemini providers
	for _, config := range geminiConfigs {
		provider, err := libmodelprovider.NewGeminiProvider(config.APIKey, config.ModelName, http.DefaultClient)
		if err == nil {
			cloudProviders["gemini"] = append(cloudProviders["gemini"], provider)
		}
	}

	// Create OpenAI providers
	for _, config := range openaiConfigs {
		provider, err := libmodelprovider.NewOpenAIProvider(config.APIKey, config.ModelName, http.DefaultClient)
		if err == nil {
			cloudProviders["openai"] = append(cloudProviders["openai"], provider)
		}
	}

	return cloudProviders
}
