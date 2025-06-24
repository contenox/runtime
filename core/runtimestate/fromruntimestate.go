package runtimestate

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/contenox/contenox/libs/libmodelprovider"
)

// ProviderFromRuntimeState retrieves available model providers for a specific backend type
type ProviderFromRuntimeState func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error)

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

	// Create all providers grouped by backend type
	providersByType := make(map[string][]libmodelprovider.Provider)
	var errC error
	for backendType, modelMap := range modelsByBackendType {
		var providers []libmodelprovider.Provider

		for modelName, baseURLs := range modelMap {
			switch backendType {
			case "ollama":
				providers = append(providers, libmodelprovider.NewOllamaModelProvider(modelName, baseURLs, http.DefaultClient))
			case "vllm":
				provider := libmodelprovider.NewVLLMModelProvider(modelName, baseURLs, http.DefaultClient)
				providers = append(providers, provider)
			default:
				errC = fmt.Errorf("SERVER BUG: unsupported backend type: %s", backendType)
			}
		}

		providersByType[backendType] = providers
	}

	// Return the runtime state function that filters by backend type
	return func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error) {
		if len(backendTypes) == 0 {
			return nil, errors.New("no backend types specified")
		}

		var filteredProviders []libmodelprovider.Provider
		for _, backendType := range backendTypes {
			if providers, ok := providersByType[backendType]; ok {
				filteredProviders = append(filteredProviders, providers...)
			}
		}

		return filteredProviders, errC
	}
}
