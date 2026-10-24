package openai

import (
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/chatcompletions"
)

const cacheRetentionExtended = 24 * time.Hour

func applyOpenAIChatControls(req *openAIChatRequest, modelName string, cfg *modelrepo.ChatConfig, thinking *modelrepo.ThinkingTypes, supportsThink bool) error {
	req.Temperature = cfg.Temperature
	req.MaxCompletionTokens = cfg.MaxTokens
	req.TopP = cfg.TopP
	req.Seed = cfg.Seed
	req.Stop = cfg.StopSequences
	req.ParallelToolCalls = cfg.ParallelToolCalls
	req.ResponseFormat = chatResponseFormat(cfg.Format)
	req.PromptCacheKey, req.PromptCacheRetention = cacheControls(cfg.CacheHints)

	if supportsThink {
		effort, err := openAIReasoningEffort(modelName, cfg.Think, thinking)
		if err != nil {
			return err
		}
		req.ReasoningEffort = effort
	}

	if openAIShouldOmitSamplingParams(modelName, req.ReasoningEffort) {
		req.Temperature = nil
		req.TopP = nil
	}
	return nil
}

func chatResponseFormat(format *modelrepo.ResponseFormat) any {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return nil
	}
	wire := &chatcompletions.ResponseFormat{Type: string(format.Mode)}
	if format.Mode == modelrepo.ResponseFormatJSONSchema {
		wire.JSONSchema = &chatcompletions.JSONSchema{
			Name:   format.Name,
			Schema: format.Schema,
			Strict: format.Strict,
		}
	}
	return wire
}

func chatToolChoice(choice *modelrepo.ToolChoice, sanitized map[string]string) any {
	if choice == nil {
		return nil
	}
	if choice.Tool == "" {
		return string(choice.Mode)
	}
	name := sanitized[choice.Tool]
	if name == "" {
		name = sanitizeToolName(choice.Tool)
	}
	return &chatcompletions.NamedToolChoice{
		Type:     "function",
		Function: chatcompletions.NamedToolChoiceName{Name: name},
	}
}

func responsesText(format *modelrepo.ResponseFormat) *openAIResponsesText {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return nil
	}
	wire := &openAIResponsesTextFormat{Type: string(format.Mode)}
	if format.Mode == modelrepo.ResponseFormatJSONSchema {
		strict := format.Strict
		wire.Name = format.Name
		wire.Schema = format.Schema
		wire.Strict = &strict
	}
	return &openAIResponsesText{Format: wire}
}

func responsesToolChoice(choice *modelrepo.ToolChoice, sanitized map[string]string) any {
	if choice == nil {
		return nil
	}
	if choice.Tool == "" {
		return string(choice.Mode)
	}
	name := sanitized[choice.Tool]
	if name == "" {
		name = sanitizeToolName(choice.Tool)
	}
	return &openAIResponsesToolChoiceName{Type: "function", Name: name}
}

func cacheControls(hints *modelrepo.CacheHints) (key, retention string) {
	if hints == nil {
		return "", ""
	}
	if hints.TTL >= cacheRetentionExtended {
		return hints.SessionKey, "24h"
	}
	if hints.TTL > 0 {
		return hints.SessionKey, "in_memory"
	}
	return hints.SessionKey, ""
}
