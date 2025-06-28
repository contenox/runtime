package runtimestate

import (
	"context"
	"net/http"

	"github.com/contenox/runtime-mvp/libs/libmodelprovider"
)

// ProviderFromRuntimeState retrieves available model providers
type ProviderFromRuntimeState func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error)

// // NewProviderAdapter creates a unified provider adapter that includes:
// // 1. Self-hosted providers (Ollama, vLLM) from runtime state
// // 2. Cloud providers (Gemini, OpenAI) from configuration
// func BADBetterProviderAdapter(
// 	ctx context.Context,
// 	runtime map[string]LLMState,
// 	configs ...serverops.ProviderConfig,
// ) ProviderFromRuntimeState {
// 	// Create self-hosted providers from runtime state
// 	selfHostedProviderFn := LocalProviderAdapter(ctx, runtime)

// 	// Create cloud providers from configuration
// 	cloudProviders := createCloudProviders(configs...)

// 	return func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error) {
// 		var providers []libmodelprovider.Provider

// 		// Get self-hosted providers
// 		if selfHostedProviderFn != nil {
// 			selfHosted, err := selfHostedProviderFn(ctx, backendTypes...)
// 			if err != nil {
// 				return nil, fmt.Errorf("error getting self-hosted providers: %w", err)
// 			}
// 			providers = append(providers, selfHosted...)
// 		}

// 		// Add cloud providers for requested types
// 		for _, t := range backendTypes {
// 			if cloudProvidersForType, ok := cloudProviders[t]; ok {
// 				providers = append(providers, cloudProvidersForType...)
// 			}
// 		}

// 		return providers, nil
// 	}
// }

// LocalProviderAdapter creates providers for self-hosted backends (Ollama, vLLM)
func LocalProviderAdapter(ctx context.Context, runtime map[string]LLMState) ProviderFromRuntimeState {
	// Create a two-level map: backendType -> modelName -> []baseURLs
	modelsByBackendType := make(map[string]map[string][]LLMState)

	for _, state := range runtime {
		backendType := state.Backend.Type

		if _, ok := modelsByBackendType[backendType]; !ok {
			modelsByBackendType[backendType] = make(map[string][]LLMState)
		}

		for _, model := range state.PulledModels {
			modelName := model.Model
			modelsByBackendType[backendType][modelName] = append(
				modelsByBackendType[backendType][modelName],
				state,
			)
		}
	}

	// Create providers grouped by backend type
	providersByType := make(map[string][]libmodelprovider.Provider)
	for backendType, modelMap := range modelsByBackendType {
		for modelName, snapshots := range modelMap {
			apiKey := ""
			backendURLs := []string{}
			for _, state := range snapshots {
				apiKey = state.apiKey
				backendURLs = append(backendURLs, state.Backend.BaseURL)
			}
			switch backendType {
			case "ollama":
				providersByType["ollama"] = append(providersByType["ollama"],
					libmodelprovider.NewOllamaModelProvider(modelName, backendURLs, http.DefaultClient))
			case "vllm":
				providersByType["vllm"] = append(providersByType["vllm"],
					libmodelprovider.NewVLLMModelProvider(modelName, backendURLs, http.DefaultClient))
			case "openai":
				providersByType["openai"] = append(providersByType["openai"],
					libmodelprovider.NewOpenAIProvider(apiKey, modelName, backendURLs, http.DefaultClient))
			case "gemini":
				{
					providersByType["gemini"] = append(providersByType["gemini"],
						libmodelprovider.NewGeminiProvider(apiKey, modelName, backendURLs, http.DefaultClient))
				}
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

// // createCloudProviders creates providers for cloud-based services (Gemini, OpenAI)
// func createCloudProviders(
// 	configs ...serverops.ProviderConfig,
// ) map[string][]libmodelprovider.Provider {
// 	cloudProviders := make(map[string][]libmodelprovider.Provider)
// 	if len(configs) == 0 {
// 		return cloudProviders
// 	}

// 	// Create Gemini providers
// 	for _, config := range configs {
// 		if config.Type == "gemini" {
// 			provider, err := libmodelprovider.NewGeminiProvider(config.APIKey, config.ModelName, http.DefaultClient)
// 			if err == nil {
// 				cloudProviders["gemini"] = append(cloudProviders["gemini"], provider)
// 			}
// 		}
// 		if config.Type == "openai" {
// 			provider, err := libmodelprovider.NewOpenAIProvider(config.APIKey, config.ModelName, http.DefaultClient)
// 			if err == nil {
// 				cloudProviders["openai"] = append(cloudProviders["openai"], provider)
// 			}
// 		}
// 	}

// 	return cloudProviders
// }
