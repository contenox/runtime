package modelprovider

import (
	"context"

	"github.com/contenox/contenox/core/runtimestate"
)

// RuntimeState retrieves available model providers for a specific backend type
type RuntimeState func(ctx context.Context, backendType string) ([]Provider, error)

func ModelProviderAdapter(ctx context.Context, runtime map[string]runtimestate.LLMState) RuntimeState {
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

	// Create all providers grouped by backend type
	providersByType := make(map[string][]Provider)

	for backendType, modelMap := range modelsByBackendType {
		var providers []Provider

		for modelName, baseURLs := range modelMap {
			switch backendType {
			case "ollama":
				providers = append(providers, NewOllamaModelProvider(modelName, baseURLs))
			case "vllm":
				providers = append(providers, NewVLLMModelProvider(modelName, baseURLs))
			}
		}

		providersByType[backendType] = providers
	}

	// Return the runtime state function that filters by backend type
	return func(ctx context.Context, backendType string) ([]Provider, error) {
		return providersByType[backendType], nil
	}
}
