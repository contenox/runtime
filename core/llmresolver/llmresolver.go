// Package llmresolver selects the most appropriate backend LLM instance based on requirements.
package llmresolver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/serverops"
)

var (
	ErrNoAvailableModels        = errors.New("no models found in runtime state")
	ErrNoSatisfactoryModel      = errors.New("no model matched the requirements")
	ErrUnknownModelCapabilities = errors.New("capabilities not known for this model")
)

// Request contains requirements for selecting a model provider.
type Request struct {
	Provider      string   // Optional: if empty, uses default provider
	ModelNames    []string // Optional: if empty, any model is considered
	ContextLength int      // Minimum required context length; 0 means no requirement
}

func filterCandidates(
	ctx context.Context,
	req Request,
	getModels modelprovider.RuntimeState,
	capCheck func(modelprovider.Provider) bool,
) ([]modelprovider.Provider, error) {
	providerType := req.Provider
	if providerType == "" {
		providerType = "Ollama" // Default provider
	}

	providers, err := getModels(ctx, providerType)
	if err != nil {
		return nil, err
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
			basePreferred := parseModelName(preferredModel)

			for _, p := range providers {
				if seenProviders[p.GetID()] {
					continue
				}

				// Match either base or full name
				currentBase := parseModelName(p.ModelName())
				currentFull := p.ModelName()

				if currentBase != basePreferred && currentFull != preferredModel {
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
		builder.WriteString(fmt.Sprintf("- provider: %q\n", providerType))
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

// parseModelName extracts the base model name before the first colon
func parseModelName(modelName string) string {
	if parts := strings.SplitN(modelName, ":", 2); len(parts) > 0 {
		return parts[0]
	}
	return modelName
}

func Chat(
	ctx context.Context,
	req Request,
	getModels modelprovider.RuntimeState,
	resolver Policy,
) (serverops.LLMChatClient, error) {
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanChat)
	if err != nil {
		return nil, err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, err
	}
	return provider.GetChatConnection(backend)
}

type EmbedRequest struct {
	ModelName string
	Provider  string // Optional. Empty uses default.
}

// Embed finds a provider supporting embeddings
func Embed(
	ctx context.Context,
	embedReq EmbedRequest,
	getModels modelprovider.RuntimeState,
	resolver Policy,
) (serverops.LLMEmbedClient, error) {
	if embedReq.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	req := Request{
		ModelNames: []string{embedReq.ModelName},
		Provider:   embedReq.Provider,
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanEmbed)
	if err != nil {
		return nil, fmt.Errorf("failed to filter candidates %w", err)
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, fmt.Errorf("failed apply resolver %w", err)
	}
	return provider.GetEmbedConnection(backend)
}

// Stream finds a provider supporting streaming
func Stream(
	ctx context.Context,
	req Request,
	getModels modelprovider.RuntimeState,
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
	return provider.GetStreamConnection(backend)
}

type PromptRequest struct {
	ModelName string
	Provider  string // Optional. Empty uses default.
}

func PromptExecute(
	ctx context.Context,
	reqExec PromptRequest,
	getModels modelprovider.RuntimeState,
	resolver Policy,
) (serverops.LLMPromptExecClient, error) {
	if reqExec.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	req := Request{
		ModelNames: []string{reqExec.ModelName},
		Provider:   reqExec.Provider,
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelprovider.Provider.CanPrompt)
	if err != nil {
		return nil, err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		return nil, err
	}
	return provider.GetPromptConnection(backend)
}
