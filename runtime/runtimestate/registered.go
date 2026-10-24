package runtimestate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelcapability"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func (s *State) ReconcileDeclarations(ctx context.Context, id string) error {
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return err
	}
	facts, err := store.GetLLMProviderModelFacts(ctx, id)
	if err != nil {
		return err
	}
	registered := []string{}
	for name, fact := range facts {
		if fact.Registered {
			registered = append(registered, name)
		}
	}
	if len(registered) == 0 {
		return nil
	}
	if backend.Type == "modeld" || backend.Type == "local" {
		return errors.New("native model registration requires owner inventory and installed artifacts")
	}
	sort.Strings(registered)
	state, observed := s.Get(ctx)[id]
	if !observed {
		state = BackendRuntimeState{ID: id, Name: backend.Name, Backend: *backend}
	}
	if state.Error != "" {
		state.DiscoveryError = state.Error
	}
	state.Error = ""
	credential, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
		return err
	}
	state.SetAPIKey(credential)
	if backend.Type == modelauth.ProviderType {
		auth := modelauth.New(s.dbInstance)
		state.authorize = func(ctx context.Context) (http.Header, error) { return auth.Headers(ctx, id) }
	}
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: backend.Type, BaseURL: backend.BaseURL, APIKey: credential}, modelrepo.WithCatalogAuthorizer(state.authorize))
	if err != nil {
		return err
	}
	for _, name := range registered {
		fact := facts[name]
		if err := runtimetypes.ValidateModelFacts(fact); err != nil {
			return err
		}
		model := ModelPullStatus{Name: name, Model: name, Registered: true, ContextLength: fact.ContextLength, MaxOutputTokens: fact.MaxOutputTokens, DeclaredCapabilities: fact.Capabilities, CompletionMode: fact.CompletionMode, FIMFormat: fact.FIMFormat, NextEditFormat: fact.NextEditFormat, AudioExtension: fact.AudioExtension}
		applyDeclaredCapabilities(&model, fact.Capabilities)
		override, exists, err := modelcapability.New(store).Get(ctx, ConnectionSelector(id), name)
		if err != nil {
			return err
		}
		if exists && override.CompletionMode != nil {
			model.CompletionMode = *override.CompletionMode
		}
		provider := catalog.ProviderFor(observedModelFromPullStatus(model))
		if provider == nil {
			return fmt.Errorf("adapter %s cannot construct the registered model %s", backend.Type, name)
		}
		model.AdapterCapabilities, err = DeclaredModelCapabilities(*backend, fact)
		if err != nil {
			return err
		}
		for _, capability := range fact.Capabilities {
			if !ModelCapabilityIsAdapterOperation(capability) {
				continue
			}
			if !slices.Contains(model.AdapterCapabilities, capability) {
				return fmt.Errorf("adapter %s does not execute declared capability %s for model %s", backend.Type, capability, name)
			}
		}
		index := slices.IndexFunc(state.PulledModels, func(candidate ModelPullStatus) bool { return declaredModelName(candidate) == name })
		if index >= 0 {
			previous := state.PulledModels[index]
			model.Digest = previous.Digest
			model.AdvertisedCapabilities = previous.AdvertisedCapabilities
			state.PulledModels[index] = model
		} else {
			state.PulledModels = append(state.PulledModels, model)
		}
		if !slices.Contains(state.Models, name) {
			state.Models = append(state.Models, name)
		}
	}
	state.Backend = *backend
	s.state.Store(id, &state)
	return nil
}

func RegistrationCapabilities(backend runtimetypes.Backend) ([]string, error) {
	if backend.Type == "modeld" || backend.Type == "local" {
		return []string{}, nil
	}
	provider, err := declarationProvider(backend, modelrepo.CapabilityConfig{CanChat: true, CanPrompt: true, CanStream: true})
	if err != nil {
		return nil, err
	}
	return providerCapabilities(provider), nil
}

func DeclaredModelCapabilities(backend runtimetypes.Backend, facts runtimetypes.ModelFacts) ([]string, error) {
	declares := func(capability string) bool { return slices.Contains(facts.Capabilities, capability) }
	provider, err := declarationProvider(backend, modelrepo.CapabilityConfig{
		CompletionMode:  facts.CompletionMode,
		FIMFormat:       facts.FIMFormat,
		CanAutocomplete: declares(runtimetypes.CapabilityAutocomplete),
		NextEditFormat:  facts.NextEditFormat,
		CanNextEdit:     declares(runtimetypes.CapabilityNextEdit),
		CanChat:         declares(runtimetypes.CapabilityCompletion),
		CanPrompt:       declares(runtimetypes.CapabilityCompletion),
		CanStream:       declares(runtimetypes.CapabilityCompletion),
		CanEmbed:        declares(runtimetypes.CapabilityEmbedding),
		CanVision:       declares(runtimetypes.CapabilityVision),
		CanAudio:        declares(runtimetypes.CapabilityAudio),
		CanThink:        declares(runtimetypes.CapabilityThinking),
		AudioExtension:  facts.AudioExtension,
	})
	if err != nil {
		return nil, err
	}
	return providerCapabilities(provider), nil
}

func ModelCapabilityIsAdapterOperation(capability string) bool {
	switch capability {
	case runtimetypes.CapabilityCompletion, runtimetypes.CapabilityAutocomplete,
		runtimetypes.CapabilityNextEdit, runtimetypes.CapabilityEmbedding,
		runtimetypes.CapabilityAudio:
		return true
	default:
		return false
	}
}

func declarationProvider(backend runtimetypes.Backend, capabilities modelrepo.CapabilityConfig) (modelrepo.Provider, error) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: backend.Type, BaseURL: backend.BaseURL}, modelrepo.WithCatalogAuthorizer(func(context.Context) (http.Header, error) {
		return nil, errors.New("capability inspection cannot authorize a request")
	}))
	if err != nil {
		return nil, err
	}
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "operator-declaration", CapabilityConfig: capabilities})
	if provider == nil {
		return nil, errors.New("adapter cannot construct declared models")
	}
	return provider, nil
}

func providerCapabilities(provider modelrepo.Provider) []string {
	capabilities := []string{}
	for _, capability := range []struct {
		name      string
		supported bool
	}{
		{runtimetypes.CapabilityCompletion, provider.CanChat() || provider.CanPrompt()},
		{runtimetypes.CapabilityAutocomplete, provider.CanAutocomplete()},
		{runtimetypes.CapabilityNextEdit, provider.CanNextEdit()},
		{runtimetypes.CapabilityTools, provider.CanChat()},
		{runtimetypes.CapabilityEmbedding, provider.CanEmbed()},
		{runtimetypes.CapabilityVision, provider.CanVision()},
		{runtimetypes.CapabilityAudio, provider.CanAudio()},
		{runtimetypes.CapabilityThinking, provider.CanThink()},
	} {
		if capability.supported {
			capabilities = append(capabilities, capability.name)
		}
	}
	return capabilities
}
