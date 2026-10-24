package runtimetypes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/contenox/runtime/libkeypool"
	"github.com/contenox/runtime/runtime/modelcontract"
	"slices"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
)

const (
	CapabilityCompletion   = "completion"
	CapabilityAutocomplete = "autocomplete"
	CapabilityNextEdit     = "next_edit"
	CapabilityTools        = "tools"
	CapabilityVision       = "vision"
	CapabilityThinking     = "thinking"
	CapabilityEmbedding    = "embedding"
	CapabilityAudio        = "audio"
)

const (
	CompletionModeFIM      = "fim"
	CompletionModeInstruct = "instruct"
	CompletionModePrompt   = "prompt"
)

func ModelCapabilities() []string {
	return []string{
		CapabilityCompletion, CapabilityAutocomplete, CapabilityNextEdit, CapabilityTools, CapabilityVision,
		CapabilityThinking, CapabilityEmbedding, CapabilityAudio,
	}
}

const (
	KeyRotationStickyClient = string(libkeypool.PolicyStickyClient)
	KeyRotationRoundRobin   = string(libkeypool.PolicyRoundRobin)
)

func KeyRotations() []string {
	policies := libkeypool.Policies()
	out := make([]string, 0, len(policies))
	for _, policy := range policies {
		out = append(out, string(policy))
	}
	return out
}

func ValidKeyRotation(name string) bool {
	return libkeypool.ValidPolicy(libkeypool.Policy(name))
}

type ModelPricing struct {
	InputPerMillion      float64 `json:"input_per_million,omitempty"`
	CacheReadPerMillion  float64 `json:"cache_read_per_million,omitempty"`
	CacheWritePerMillion float64 `json:"cache_write_per_million,omitempty"`
	OutputPerMillion     float64 `json:"output_per_million,omitempty"`
	PerImage             float64 `json:"per_image,omitempty"`
	PerAudioMebibyte     float64 `json:"per_audio_mebibyte,omitempty"`
}

func (p ModelPricing) CalculateCost(promptTokens, cacheReadTokens, cacheWriteTokens, completionTokens, images, audioBytes int64) float64 {
	uncachedInput := promptTokens - cacheReadTokens
	if uncachedInput < 0 {
		uncachedInput = 0
	}
	inputCost := (float64(uncachedInput) / 1_000_000.0) * p.InputPerMillion
	cacheReadCost := (float64(cacheReadTokens) / 1_000_000.0) * p.CacheReadPerMillion
	cacheWriteCost := (float64(cacheWriteTokens) / 1_000_000.0) * p.CacheWritePerMillion
	outputCost := (float64(completionTokens) / 1_000_000.0) * p.OutputPerMillion
	imageCost := float64(images) * p.PerImage
	audioCost := (float64(audioBytes) / Mebibyte) * p.PerAudioMebibyte
	return inputCost + cacheReadCost + cacheWriteCost + outputCost + imageCost + audioCost
}

const Mebibyte = 1 << 20

type ModelFacts struct {
	Registered      bool          `json:"registered,omitempty"`
	CompletionMode  string        `json:"completion_mode,omitempty"`
	FIMFormat       string        `json:"fim_format,omitempty"`
	NextEditFormat  string        `json:"next_edit_format,omitempty"`
	AudioExtension  bool          `json:"audio_extension,omitempty"`
	ContextLength   int           `json:"context_length,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Capabilities    []string      `json:"capabilities,omitempty"`
	Pricing         *ModelPricing `json:"pricing,omitempty"`
}

func (f ModelFacts) IsZero() bool {
	return !f.Registered && !f.AudioExtension && f.CompletionMode == "" && f.FIMFormat == "" && f.NextEditFormat == "" && f.ContextLength == 0 && f.MaxOutputTokens == 0 && len(f.Capabilities) == 0 && f.Pricing == nil
}

func ValidateModelFacts(facts ModelFacts) error {
	if facts.CompletionMode != "" && facts.CompletionMode != CompletionModeInstruct && facts.CompletionMode != CompletionModePrompt && facts.CompletionMode != CompletionModeFIM {
		return errors.New("completion_mode must be fim, instruct, prompt or empty")
	}
	if facts.CompletionMode == CompletionModeFIM {
		if _, err := modelcontract.ResolveFIM(facts.FIMFormat); err != nil {
			return err
		}
		if !slices.Contains(facts.Capabilities, CapabilityAutocomplete) {
			return errors.New("native FIM requires the autocomplete capability")
		}
	} else if facts.FIMFormat != "" || slices.Contains(facts.Capabilities, CapabilityAutocomplete) {
		return errors.New("the native autocomplete capability requires completion_mode fim and its explicit FIM format")
	}
	if facts.CompletionMode == "prompt" && !slices.Contains(facts.Capabilities, CapabilityCompletion) {
		return errors.New("prompt completion requires the completion capability")
	}
	if facts.NextEditFormat != "" {
		if _, err := modelcontract.ResolveNextEdit(facts.NextEditFormat); err != nil {
			return err
		}
		if !slices.Contains(facts.Capabilities, CapabilityNextEdit) {
			return errors.New("the next_edit capability requires its explicit next_edit_format")
		}
	} else if slices.Contains(facts.Capabilities, CapabilityNextEdit) {
		return errors.New("the next_edit capability requires its explicit next_edit_format")
	}
	if facts.Registered && (facts.ContextLength <= 0 || len(facts.Capabilities) == 0) {
		return errors.New("model registration requires an explicit context length and capabilities")
	}
	if facts.ContextLength < 0 {
		return fmt.Errorf("context_length %d is negative", facts.ContextLength)
	}
	if facts.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens %d is negative", facts.MaxOutputTokens)
	}
	for _, capability := range facts.Capabilities {
		if !ValidModelCapability(capability) {
			return fmt.Errorf("capability %q is not one of %s",
				capability, strings.Join(ModelCapabilities(), ", "))
		}
	}
	if p := facts.Pricing; p != nil {
		if p.InputPerMillion < 0 || p.CacheReadPerMillion < 0 || p.CacheWritePerMillion < 0 || p.OutputPerMillion < 0 || p.PerImage < 0 || p.PerAudioMebibyte < 0 {
			return errors.New("pricing rates must be non-negative")
		}
	}
	return nil
}

func ValidModelCapability(name string) bool {
	for _, known := range ModelCapabilities() {
		if name == known {
			return true
		}
	}
	return false
}

func (s *store) SetLLMProviderModelFacts(ctx context.Context, providerID string, facts map[string]ModelFacts) error {
	if providerID == "" {
		return libdb.ErrNotFound
	}
	stored := make(map[string]ModelFacts, len(facts))
	for model, modelFacts := range facts {
		if strings.TrimSpace(model) == "" || modelFacts.IsZero() {
			continue
		}
		if err := ValidateModelFacts(modelFacts); err != nil {
			return fmt.Errorf("store: model %s: %w", model, err)
		}
		stored[model] = modelFacts
	}
	if len(stored) == 0 {
		return s.DeleteLLMProviderModelFacts(ctx, providerID)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("store: encode provider model facts: %w", err)
	}
	_, err = s.ExecContext(ctx, `
		INSERT INTO llm_provider_model_facts (provider_id, facts, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider_id) DO UPDATE SET
			facts = excluded.facts,
			updated_at = excluded.updated_at
	`, providerID, string(raw), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("store: set provider model facts: %w", err)
	}
	return nil
}

func (s *store) SetLLMProviderModelFact(ctx context.Context, providerID, model string, facts ModelFacts) error {
	model = strings.TrimSpace(model)
	if providerID == "" || model == "" {
		return libdb.ErrNotFound
	}
	if facts.IsZero() {
		return s.DeleteLLMProviderModelFact(ctx, providerID, model)
	}
	if err := ValidateModelFacts(facts); err != nil {
		return fmt.Errorf("store: model %s: %w", model, err)
	}
	declared, err := s.GetLLMProviderModelFacts(ctx, providerID)
	if err != nil {
		return err
	}
	declared[model] = facts
	return s.SetLLMProviderModelFacts(ctx, providerID, declared)
}

func (s *store) DeleteLLMProviderModelFact(ctx context.Context, providerID, model string) error {
	model = strings.TrimSpace(model)
	if providerID == "" || model == "" {
		return libdb.ErrNotFound
	}
	declared, err := s.GetLLMProviderModelFacts(ctx, providerID)
	if err != nil {
		return err
	}
	if _, ok := declared[model]; !ok {
		return nil
	}
	delete(declared, model)
	return s.SetLLMProviderModelFacts(ctx, providerID, declared)
}

func (s *store) DeleteLLMProviderModelFacts(ctx context.Context, providerID string) error {
	if providerID == "" {
		return libdb.ErrNotFound
	}
	if _, err := s.ExecContext(ctx, `
		DELETE FROM llm_provider_model_facts WHERE provider_id = $1
	`, providerID); err != nil {
		return fmt.Errorf("store: delete provider model facts: %w", err)
	}
	return nil
}

func (s *store) GetLLMProviderModelFacts(ctx context.Context, providerID string) (map[string]ModelFacts, error) {
	if providerID == "" {
		return nil, libdb.ErrNotFound
	}
	var raw string
	err := s.QueryRowContext(ctx, `
		SELECT facts FROM llm_provider_model_facts WHERE provider_id = $1
	`, providerID).Scan(&raw)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return map[string]ModelFacts{}, nil
		}
		return nil, fmt.Errorf("store: get provider model facts: %w", err)
	}
	facts := map[string]ModelFacts{}
	if raw == "" {
		return facts, nil
	}
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return nil, fmt.Errorf("store: decode provider model facts: %w", err)
	}
	return facts, nil
}
