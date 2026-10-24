package runtimestate

import (
	"context"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

func LocalProviderAdapter(ctx context.Context, tracker libtracker.ActivityTracker, runtime map[string]BackendRuntimeState) ProviderFromRuntimeState {
	providersByKey := make(map[string][]modelrepo.Provider)
	var all []modelrepo.Provider

	for _, state := range runtime {
		if state.Error != "" {
			continue
		}

		backendType := modelrepo.CanonicalBackendType(state.Backend.Type)
		catalog, err := modelrepo.NewCatalogProvider(
			modelrepo.BackendSpec{
				Type:    backendType,
				BaseURL: state.Backend.BaseURL,
				APIKey:  state.GetAPIKey(),
			},
			modelrepo.WithCatalogHTTPClient(modelrepo.SharedHTTPClient),
			modelrepo.WithCatalogTracker(tracker),
			modelrepo.WithCatalogAuthorizer(state.authorize),
		)
		if err != nil {
			continue
		}

		keys := []string{backendType, ConnectionSelector(state.Backend.ID)}
		if name := strings.ToLower(strings.TrimSpace(state.Backend.Name)); name != "" && name != backendType {
			keys = append(keys, name)
		}

		for _, model := range state.PulledModels {
			p := catalog.ProviderFor(observedModelFromPullStatus(model))
			if p == nil {
				continue
			}
			all = append(all, p)
			for _, key := range keys {
				providersByKey[key] = append(providersByKey[key], p)
			}
		}
	}

	return func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error) {
		hasNonEmpty := false
		for _, bt := range backendTypes {
			if bt != "" {
				hasNonEmpty = true
				break
			}
		}
		if !hasNonEmpty {
			return all, nil
		}
		var providers []modelrepo.Provider
		seen := make(map[string]bool, len(backendTypes))
		for _, backendType := range backendTypes {
			backendType = modelrepo.CanonicalBackendType(backendType)
			if backendType == "" || seen[backendType] {
				continue
			}
			seen[backendType] = true
			providers = append(providers, providersByKey[backendType]...)
		}
		return providers, nil
	}
}

type ProviderFromRuntimeState func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error)
