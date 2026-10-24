package runtimestate

import (
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const observedDisplayNameMetaKey = "display_name"

func observedModelFromPullStatus(model ModelPullStatus) modelrepo.ObservedModel {
	name := strings.TrimSpace(model.Model)
	if name == "" {
		name = strings.TrimSpace(model.Name)
	}

	meta := map[string]string{}
	if model.NativeBackend != "" {
		meta["type"] = model.NativeBackend
	}
	if display := strings.TrimSpace(model.Name); display != "" && display != name {
		meta[observedDisplayNameMetaKey] = display
	}
	if len(meta) == 0 {
		meta = nil
	}

	return modelrepo.ObservedModel{
		Name:          name,
		ContextLength: model.ContextLength,
		ModifiedAt:    model.ModifiedAt,
		Size:          model.Size,
		Digest:        model.Digest,
		CapabilityConfig: modelrepo.CapabilityConfig{
			CompletionMode: model.CompletionMode,
			FIMFormat:      model.FIMFormat, CanAutocomplete: model.CanAutocomplete,
			NextEditFormat: model.NextEditFormat, CanNextEdit: model.CanNextEdit,
			ContextLength:    model.ContextLength,
			MaxOutputTokens:  model.MaxOutputTokens,
			CanChat:          model.CanChat,
			CanEmbed:         model.CanEmbed,
			CanPrompt:        model.CanPrompt,
			CanStream:        model.CanStream,
			CanThink:         model.CanThink,
			CanVision:        model.CanVision,
			CanAudio:         model.CanAudio,
			AudioExtension:   model.AudioExtension,
			SessionExtension: model.SessionExtension,
			ThinkingTypes:    model.ThinkingTypes,
			PromptCache:      model.PromptCache,
		},
		Meta: meta,
	}
}

func declaredModelsByName(models []*runtimetypes.Model) map[string]*runtimetypes.Model {
	byName := make(map[string]*runtimetypes.Model, len(models))
	for _, model := range models {
		byName[model.Model] = model
	}
	return byName
}

func declaredModelNames(models []*runtimetypes.Model) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Model)
	}
	return names
}

func pullStatusFromObservedModel(model modelrepo.ObservedModel) ModelPullStatus {
	displayName := model.Name
	if model.Meta != nil {
		if display := strings.TrimSpace(model.Meta[observedDisplayNameMetaKey]); display != "" {
			displayName = display
		}
	}

	return ModelPullStatus{
		AdvertisedCapabilities: observedCapabilities(model.CapabilityConfig),
		NativeBackend:          model.Meta["type"],
		CompletionMode:         model.CompletionMode,
		FIMFormat:              model.FIMFormat, CanAutocomplete: model.CanAutocomplete,
		NextEditFormat: model.NextEditFormat, CanNextEdit: model.CanNextEdit,
		Name:             displayName,
		Model:            model.Name,
		ModifiedAt:       model.ModifiedAt,
		Size:             model.Size,
		Digest:           model.Digest,
		ContextLength:    model.ContextLength,
		MaxOutputTokens:  model.MaxOutputTokens,
		CanChat:          model.CanChat,
		CanEmbed:         model.CanEmbed,
		CanPrompt:        model.CanPrompt,
		CanStream:        model.CanStream,
		CanThink:         model.CanThink,
		CanVision:        model.CanVision,
		CanAudio:         model.CanAudio,
		AudioExtension:   model.CapabilityConfig.AudioExtension,
		SessionExtension: model.CapabilityConfig.SessionExtension,
		ThinkingTypes:    model.CapabilityConfig.ThinkingTypes,
		PromptCache:      model.CapabilityConfig.PromptCache,
	}
}

func observedCapabilities(config modelrepo.CapabilityConfig) []string {
	capabilities := []string{}
	for _, capability := range []struct {
		name      string
		supported bool
	}{
		{runtimetypes.CapabilityCompletion, config.CanChat || config.CanPrompt},
		{runtimetypes.CapabilityAutocomplete, config.CanAutocomplete},
		{runtimetypes.CapabilityNextEdit, config.CanNextEdit},
		{runtimetypes.CapabilityTools, config.CanChat},
		{runtimetypes.CapabilityEmbedding, config.CanEmbed},
		{runtimetypes.CapabilityVision, config.CanVision},
		{runtimetypes.CapabilityAudio, config.CanAudio},
		{runtimetypes.CapabilityThinking, config.CanThink},
	} {
		if capability.supported {
			capabilities = append(capabilities, capability.name)
		}
	}
	return capabilities
}
