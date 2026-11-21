package modeld

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

type catalogProvider struct {
	spec    modelrepo.BackendSpec
	tracker libtracker.ActivityTracker
}

func init() {
	constructor := func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		return newCatalogProvider(spec, opts), nil
	}
	modelrepo.RegisterCatalogProvider("modeld", constructor)
	modelrepo.RegisterCatalogProvider("local", constructor)
}
func newCatalogProvider(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) modelrepo.CatalogProvider {
	return &catalogProvider{spec: spec, tracker: opts.Tracker}
}
func (p *catalogProvider) Type() string { return "modeld" }
func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	target := resolveTarget(p.spec.BaseURL)
	if target.Endpoint == "" {
		return p.localModels(ctx)
	}
	inventory, err := modeldconn.InventoryTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	models, err := modeldconn.ListModelsTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	out := make([]modelrepo.ObservedModel, 0, len(models))
	for _, m := range models {
		if m.Pipeline != "" {
			out = append(out, observedMedia(m, inventory))
			continue
		}
		config := transport.Config{Execution: &transport.Execution{Role: "inspection", Priority: "background"}}
		descriptor, resolveErr := modelregistry.NewCurated().Resolve(ctx, m.Name)
		if resolveErr != nil && !errors.Is(resolveErr, modelregistry.ErrNotFound) {
			return nil, resolveErr
		}
		if descriptor != nil && descriptor.SupportsRetrieval() {
			config.EmbeddingPooling = descriptor.Embedding.Pooling
			config.NumCtx = descriptor.Embedding.ContextTokens
		}
		info, err := modeldconn.DescribeTarget(ctx, target, modeldconn.ModelRef{Name: m.Name, Type: m.Type, Digest: m.Digest}, config)
		if err != nil {
			return nil, fmt.Errorf("describe modeld model %s: %w", m.Name, err)
		}
		caps := capabilitiesFromInfo(info)
		if desc, resolveErr := modelregistry.NewCurated().Resolve(ctx, m.Name); resolveErr == nil {
			caps.CanEmbed = caps.CanEmbed && desc.SupportsRetrieval()
			if desc.SupportsNextEdit() {
				caps.NextEditFormat = desc.NextEdit.Format
				caps.CanNextEdit = slices.ContainsFunc(inventory.Backends, func(backend transport.BackendInventory) bool {
					return backend.Backend == m.Type && backend.State == "available" && slices.Contains(backend.Operations, "next-edit")
				})
				caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
			}
			if desc.SupportsCompletion() {
				caps.CompletionMode = desc.Completion.Mode
				caps.FIMFormat = desc.Completion.FIMFormat
				caps.CanAutocomplete = desc.Completion.Mode == modelregistry.CompletionFIM
				if caps.CanAutocomplete {
					caps.CanAutocomplete = slices.ContainsFunc(inventory.Backends, func(backend transport.BackendInventory) bool {
						return backend.Backend == m.Type && backend.State == "available" && slices.Contains(backend.Operations, "autocomplete")
					})
					caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
				}
			}
			if desc.SupportsRetrieval() {
				caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
			}
		} else if !errors.Is(resolveErr, modelregistry.ErrNotFound) {
			return nil, resolveErr
		}
		out = append(out, modelrepo.ObservedModel{Name: m.Name, ContextLength: info.EffectiveContext, Size: m.SizeBytes, Digest: m.Digest, CapabilityConfig: caps, Meta: map[string]string{"type": m.Type}})
	}
	return out, nil
}

func capabilitiesFromInfo(info transport.ModelInfo) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		ContextLength: info.EffectiveContext,
		CanChat:       true,
		CanPrompt:     true,
		CanStream:     true,
		CanEmbed:      info.SupportsEmbedding,
		CanThink:      info.ChatTemplateSupportsThinking,
		CanVision:     info.SupportsVision,
	}
}

func (p *catalogProvider) localModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	endpoint, err := modeldconn.LocalEndpointAddr(ctx)
	if err != nil {
		return nil, err
	}
	client, err := modeldconn.Endpoint(ctx, "catalog:"+modeldconn.DataRootFromContext(ctx), endpoint)
	if err != nil {
		return nil, err
	}
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return nil, fmt.Errorf("local capability inventory unavailable; "+remedy.UpdateLocal+": %w", err)
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]modelrepo.ObservedModel, 0, len(models))
	for _, m := range models {
		if m.Pipeline != "" {
			out = append(out, observedMedia(m, inventory))
			continue
		}
		backend := transport.BackendInventory{Backend: m.Type, State: "unavailable", Error: "This backend is not installed; " + remedy.PrepareLocal + "."}
		for _, candidate := range inventory.Backends {
			if candidate.Backend == m.Type {
				backend = candidate
				break
			}
		}
		chat := backend.State == "available" && slices.Contains(backend.Operations, "chat")
		caps := modelrepo.CapabilityConfig{ContextLength: m.ContextLength, CanChat: chat, CanPrompt: chat, CanStream: chat,
			CanEmbed: backend.State == "available" && slices.Contains(backend.Operations, "embedding")}
		desc, err := modelregistry.New(nil).Resolve(ctx, m.Name)
		if err != nil && !errors.Is(err, modelregistry.ErrNotFound) {
			return nil, err
		}
		if desc != nil {
			caps.CanEmbed = caps.CanEmbed && desc.SupportsRetrieval()
			if desc.SupportsNextEdit() {
				caps.NextEditFormat = desc.NextEdit.Format
				caps.CanNextEdit = backend.State == "available" && slices.Contains(backend.Operations, "next-edit")
				caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
			}
			if desc.SupportsCompletion() {
				caps.CompletionMode = desc.Completion.Mode
				caps.FIMFormat = desc.Completion.FIMFormat
				caps.CanAutocomplete = desc.Completion.Mode == modelregistry.CompletionFIM && backend.State == "available" && slices.Contains(backend.Operations, "autocomplete")
				if desc.Completion.Mode == modelregistry.CompletionFIM {
					caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
				}
			}
			if desc.SupportsRetrieval() {
				caps.CanChat, caps.CanPrompt, caps.CanStream = false, false, false
			}
			caps.CanVision = chat && desc.Vision && slices.Contains(backend.Operations, "vision")
			caps.CanThink = chat && (desc.ReasoningProtocol != "" || desc.ReasoningFormat != "")
			if caps.ContextLength == 0 {
				caps.ContextLength = desc.AutoContextLimit
			}
		} else {
			caps.CanEmbed = false
		}
		out = append(out, modelrepo.ObservedModel{Name: m.Name, ContextLength: caps.ContextLength, Size: m.SizeBytes, Digest: m.Digest, CapabilityConfig: caps,
			Meta: map[string]string{"type": m.Type, "backend_state": backend.State, "backend_error": backend.Error}})
	}
	return out, nil
}
func observedMedia(model transport.NodeModel, inventory transport.Inventory) modelrepo.ObservedModel {
	role := transport.MediaRoleForPipeline(model.Pipeline)
	meta := map[string]string{"type": model.Type}
	if model.MediaError != "" {
		meta["backend_error"] = model.MediaError
	} else if slices.ContainsFunc(inventory.Backends, func(backend transport.BackendInventory) bool {
		return backend.Backend == model.Type && backend.State == "available" && slices.Contains(backend.Operations, role)
	}) {
		meta["media_role"] = role
	}
	return modelrepo.ObservedModel{Name: model.Name, Size: model.SizeBytes, Digest: model.Digest, Meta: meta}
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	provider := newModeldProvider(model.Name, []string{p.spec.BaseURL}, model.CapabilityConfig, p.tracker)
	provider.artifact = modeldconn.ModelRef{Name: model.Name, Type: model.Meta["type"], Digest: model.Digest}
	return provider
}
