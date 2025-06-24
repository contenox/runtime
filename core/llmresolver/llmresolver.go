// Package llmresolver selects the most appropriate backend LLM instance based on requirements.
package llmresolver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
)

var (
	ErrNoAvailableModels        = errors.New("no models found in runtime state")
	ErrNoSatisfactoryModel      = errors.New("no model matched the requirements")
	ErrUnknownModelCapabilities = errors.New("capabilities not known for this model")
)

var DefaultProviderType string = "ollama"

// Request contains requirements for selecting a model provider.
type Request struct {
	ProviderTypes []string // Optional: if empty, uses all default providers
	ModelNames    []string // Optional: if empty, any model is considered
	ContextLength int      // Minimum required context length
}

func filterCandidates(
	ctx context.Context,
	req Request,
	getModels runtimestate.ProviderFromRuntimeState,
	capCheck func(modelprovider.Provider) bool,
) ([]modelprovider.Provider, error) {
	providerTypes := req.ProviderTypes
	if len(providerTypes) == 0 {
		providerTypes = []string{"ollama", "vllm"}
	}
	providers, err := getModels(ctx, providerTypes...)
	if err != nil {
		return nil, fmt.Errorf("failed to get models: %w", err)
	}
	if len(providers) == 0 {
		return nil, ErrNoAvailableModels
	}

	// Use a map to track seen providers by ID to prevent duplicates
	seenProviders := make(map[string]bool)
	var candidates []modelprovider.Provider

	// Handle model name preferences
	if len(req.ModelNames) > 0 {
		// Check preferred models in order of priority
		for _, preferredModel := range req.ModelNames {
			// Use normalized model names for matching
			normalizedPreferred := NormalizeModelName(preferredModel)

			for _, p := range providers {
				if seenProviders[p.GetID()] {
					continue
				}

				// Normalize provider's model name for comparison
				currentNormalized := NormalizeModelName(p.ModelName())
				currentFull := p.ModelName()

				// Match either normalized or full name
				if currentNormalized != normalizedPreferred && currentFull != preferredModel {
					continue
				}

				if validateProvider(p, req.ContextLength, capCheck) {
					candidates = append(candidates, p)
					seenProviders[p.GetID()] = true
				}
			}
		}
	} else {
		// Consider all providers when no model names specified
		for _, p := range providers {
			if validateProvider(p, req.ContextLength, capCheck) {
				candidates = append(candidates, p)
			}
		}
	}

	if len(candidates) == 0 {
		var builder strings.Builder

		builder.WriteString("no models matched requirements:\n")
		builder.WriteString(fmt.Sprintf("- provider: %q\n", providerTypes))
		builder.WriteString(fmt.Sprintf("- model names: %v\n", req.ModelNames))
		builder.WriteString(fmt.Sprintf("- required context length: %d\n", req.ContextLength))

		builder.WriteString("- available models:\n")
		for _, p := range providers {
			builder.WriteString(fmt.Sprintf("  • %s (ID: %s, context: %d, canchat: %v, can embed: %v, canprompt: %v)\n",
				p.ModelName(), p.GetID(), p.GetContextLength(), p.CanChat(), p.CanEmbed(), p.CanPrompt()))
		}

		return nil, fmt.Errorf("%w\n%s", ErrNoSatisfactoryModel, builder.String())
	}

	return candidates, nil
}

type Policy func(candidates []modelprovider.Provider) (modelprovider.Provider, string, error)

const (
	StrategyRandom      = "random"
	StrategyAuto        = "auto"
	StrategyLowLatency  = "low-latency"
	StrategyLowPriority = "low-prio"
)

// PolicyFromString maps string names to resolver policies
func PolicyFromString(name string) (Policy, error) {
	switch strings.ToLower(name) {
	case StrategyRandom:
		return Randomly, nil
	case StrategyLowLatency, StrategyAuto:
		return HighestContext, nil
	// case StrategyLowPriority:
	// 	return ResolveLowestPriority, nil
	default:
		return nil, fmt.Errorf("unknown resolver strategy: %s", name)
	}
}

func Randomly(candidates []modelprovider.Provider) (modelprovider.Provider, string, error) {
	provider, err := selectRandomProvider(candidates)
	if err != nil {
		return nil, "", err
	}

	backend, err := selectRandomBackend(provider)
	if err != nil {
		return nil, "", err
	}

	return provider, backend, nil
}

func selectRandomProvider(candidates []modelprovider.Provider) (modelprovider.Provider, error) {
	if len(candidates) == 0 {
		return nil, ErrNoSatisfactoryModel
	}

	return candidates[rand.Intn(len(candidates))], nil
}

func selectRandomBackend(provider modelprovider.Provider) (string, error) {
	if provider == nil {
		return "", ErrNoSatisfactoryModel
	}

	backendIDs := provider.GetBackendIDs()
	if len(backendIDs) == 0 {
		return "", ErrNoSatisfactoryModel
	}

	return backendIDs[rand.Intn(len(backendIDs))], nil
}

func HighestContext(candidates []modelprovider.Provider) (modelprovider.Provider, string, error) {
	if len(candidates) == 0 {
		return nil, "", ErrNoSatisfactoryModel
	}

	var bestProvider modelprovider.Provider = nil
	maxContextLength := -1

	for _, p := range candidates {
		currentContextLength := p.GetContextLength()
		if currentContextLength > maxContextLength {
			maxContextLength = currentContextLength
			bestProvider = p
		}
	}

	if bestProvider == nil {
		return nil, "", errors.New("failed to select a provider based on context length") // Should never happen
	}

	// Once the best provider is selected, choose a backend randomly for it
	backend, err := selectRandomBackend(bestProvider)
	if err != nil {
		return nil, "", err
	}

	return bestProvider, backend, nil
}

// validateProvider checks if a provider meets requirements
func validateProvider(p modelprovider.Provider, minContext int, capCheck func(modelprovider.Provider) bool) bool {
	if minContext > 0 && p.GetContextLength() < minContext {
		return false
	}
	return capCheck(p)
}

// NormalizeModelName standardizes model names for comparison
func NormalizeModelName(modelName string) string {
	// Convert to lowercase for case-insensitive comparison
	normalized := strings.ToLower(modelName)

	// Remove common prefixes and suffixes
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, ".", "")

	// Remove organization prefix if present
	if parts := strings.Split(normalized, "/"); len(parts) > 1 {
		normalized = parts[1]
	}

	// Remove quantization suffixes
	normalized = strings.ReplaceAll(normalized, "awq", "")
	normalized = strings.ReplaceAll(normalized, "gptq", "")
	normalized = strings.ReplaceAll(normalized, "4bit", "")
	normalized = strings.ReplaceAll(normalized, "fp16", "")

	// Remove version numbers
	if idx := strings.LastIndex(normalized, ":"); idx != -1 {
		normalized = normalized[:idx]
	}

	return normalized
}

func Chat(
	ctx context.Context,
	req Request,
	getModels runtimestate.ProviderFromRuntimeState,
	resolver Policy,
) (serverops.LLMChatClient, string, error) {
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanChat)
	if err != nil {
		return nil, "", err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, "", err
	}
	if req.ContextLength == 0 {
		return nil, "", fmt.Errorf("context length must be greater than 0")
	}
	if req.ContextLength < 0 {
		return nil, "", fmt.Errorf("context length must be non-negative")
	}
	modelName := provider.ModelName()
	client, err := provider.GetChatConnection(ctx, backend)
	if err != nil {
		return nil, "", err
	}
	return client, modelName, nil
}

type EmbedRequest struct {
	ModelName    string
	ProviderType string // Optional. Empty uses default.
}

// Embed finds a provider supporting embeddings
func Embed(
	ctx context.Context,
	embedReq EmbedRequest,
	getModels runtimestate.ProviderFromRuntimeState,
	resolver Policy,
) (serverops.LLMEmbedClient, error) {
	if embedReq.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if embedReq.ProviderType == "" {
		embedReq.ProviderType = DefaultProviderType
	}
	req := Request{
		ModelNames:    []string{embedReq.ModelName},
		ProviderTypes: []string{embedReq.ProviderType},
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanEmbed)
	if err != nil {
		return nil, fmt.Errorf("failed to filter candidates %w", err)
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, fmt.Errorf("failed apply resolver %w", err)
	}
	return provider.GetEmbedConnection(ctx, backend)
}

// Stream finds a provider supporting streaming
func Stream(
	ctx context.Context,
	req Request,
	getModels runtimestate.ProviderFromRuntimeState,
	resolver Policy,
) (serverops.LLMStreamClient, error) {
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanStream)
	if err != nil {
		return nil, err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, err
	}
	return provider.GetStreamConnection(ctx, backend)
}

type PromptRequest struct {
	ModelName     string
	ProviderTypes []string // Optional. Empty uses default.
}

func PromptExecute(
	ctx context.Context,
	reqExec PromptRequest,
	getModels runtimestate.ProviderFromRuntimeState,
	resolver Policy,
) (serverops.LLMPromptExecClient, error) {
	if reqExec.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if len(reqExec.ProviderTypes) == 0 {
		reqExec.ProviderTypes = []string{DefaultProviderType, "vllm"}
	}
	req := Request{
		ModelNames:    []string{reqExec.ModelName},
		ProviderTypes: reqExec.ProviderTypes,
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanPrompt)
	if err != nil {
		return nil, err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, err
	}
	return provider.GetPromptConnection(ctx, backend)
}
