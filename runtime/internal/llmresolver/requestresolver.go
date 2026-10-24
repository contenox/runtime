package llmresolver

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

func filterCandidates(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	capCheck func(modelrepo.Provider) bool,
) ([]modelrepo.Provider, error) {
	providerTypes := req.ProviderTypes
	providers, err := getModels(ctx, providerTypes...)
	if err != nil {
		return nil, fmt.Errorf("failed to get models: %w", err)
	}
	if len(providers) == 0 {
		return nil, ErrNoAvailableModels
	}

	seenProviders := make(map[string]bool)
	var candidates []modelrepo.Provider

	if len(req.ModelNames) > 0 {
		for _, preferredModel := range req.ModelNames {
			normalizedPreferred := NormalizeModelName(preferredModel)
			var exact, loose []modelrepo.Provider
			for _, p := range providers {
				if seenProviders[candidateKey(p)] || !validateProvider(p, req.ContextLength, capCheck) {
					continue
				}
				switch {
				case p.ModelName() == preferredModel:
					exact = append(exact, p)
				case NormalizeModelName(p.ModelName()) == normalizedPreferred:
					loose = append(loose, p)
				}
			}
			matched := exact
			if len(matched) == 0 {
				matched = loose
			}
			for _, p := range matched {
				candidates = append(candidates, p)
				seenProviders[candidateKey(p)] = true
			}
		}
	} else {
		for _, p := range providers {
			if validateProvider(p, req.ContextLength, capCheck) {
				candidates = append(candidates, p)
			}
		}
	}

	if len(candidates) == 0 {
		if req.ContextLength > 0 {
			largest := 0
			var largestName string
			capable := false
			for _, p := range providers {
				if !capCheck(p) {
					continue
				}
				if len(req.ModelNames) > 0 && !providerMatchesAnyName(p, req.ModelNames) {
					continue
				}
				capable = true
				if cl := p.GetContextLength(); cl > largest {
					largest = cl
					largestName = p.ModelName()
				}
			}
			if capable && largest > 0 && largest < req.ContextLength {
				requested := modelrepo.RequestedContextLengthFromContext(ctx)
				if !req.FitContextToModel || requested > largest {
					return nil, fmt.Errorf("%w: requested context capacity is %d tokens but the largest matching model %q provides only %d; choose a larger-context model or lower the requested context window",
						ErrNoSatisfactoryModel, req.ContextLength, largestName, largest)
				}
				req.ContextLength = largest
				for _, p := range providers {
					if !validateProvider(p, req.ContextLength, capCheck) {
						continue
					}
					if len(req.ModelNames) > 0 && !providerMatchesAnyName(p, req.ModelNames) {
						continue
					}
					if seenProviders[candidateKey(p)] {
						continue
					}
					candidates = append(candidates, p)
					seenProviders[candidateKey(p)] = true
				}
			}
		}

		if len(candidates) == 0 {
			var builder strings.Builder

			builder.WriteString("no models matched requirements:\n")
			builder.WriteString(fmt.Sprintf("- provider: %q\n", providerTypes))
			builder.WriteString(fmt.Sprintf("- model names: %v\n", req.ModelNames))
			builder.WriteString(fmt.Sprintf("- required context length: %d\n", req.ContextLength))
			if req.RequiresVision {
				builder.WriteString("- requires vision (request carries images)\n")
			}
			if req.RequiresAudio {
				builder.WriteString("- requires audio (request carries audio attachments)\n")
			}

			builder.WriteString("- available models:\n")
			for _, p := range providers {
				builder.WriteString(fmt.Sprintf("  • %s (ID: %s, context: %d, canchat: %v, can embed: %v, canprompt: %v, canvision: %v, canaudio: %v)\n",
					p.ModelName(), p.GetID(), p.GetContextLength(), p.CanChat(), p.CanEmbed(), p.CanPrompt(), p.CanVision(), p.CanAudio()))
			}

			return nil, fmt.Errorf("%w\n%s", ErrNoSatisfactoryModel, builder.String())
		}
	}

	return candidates, nil
}

func candidateKey(p modelrepo.Provider) string {
	return p.GetID() + "\x00" + strings.Join(p.GetBackendIDs(), "\x00")
}

func providerMatchesAnyName(p modelrepo.Provider, names []string) bool {
	full := p.ModelName()
	normalized := NormalizeModelName(full)
	for _, name := range names {
		if full == name || normalized == NormalizeModelName(name) {
			return true
		}
	}
	return false
}

func validateProvider(p modelrepo.Provider, minContext int, capCheck func(modelrepo.Provider) bool) bool {
	cl := p.GetContextLength()
	if minContext > 0 && cl > 0 && cl < minContext {
		return false
	}
	return capCheck(p)
}

func NormalizeModelName(modelName string) string {
	normalized := strings.ToLower(modelName)

	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, ".", "")

	if parts := strings.Split(normalized, "/"); len(parts) > 1 {
		normalized = parts[1]
	}

	normalized = strings.ReplaceAll(normalized, "awq", "")
	normalized = strings.ReplaceAll(normalized, "gptq", "")
	normalized = strings.ReplaceAll(normalized, "4bit", "")
	normalized = strings.ReplaceAll(normalized, "fp16", "")

	if idx := strings.LastIndex(normalized, ":"); idx != -1 {
		normalized = normalized[:idx]
	}

	return normalized
}

func refusePinnedNonVision(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	base func(modelrepo.Provider) bool,
) error {
	if !req.RequiresVision || len(req.ModelNames) == 0 {
		return nil
	}
	providers, err := getModels(ctx, req.ProviderTypes...)
	if err != nil {
		return nil
	}
	for _, name := range req.ModelNames {
		matched := false
		for _, p := range providers {
			if !providerMatchesAnyName(p, []string{name}) || !base(p) {
				continue
			}
			matched = true
			if p.CanVision() {
				return nil
			}
		}
		if matched {
			return fmt.Errorf("%w: requested model %q accepts text only and the request carries images; name a vision-capable model, set a vision-capable default, or drop the model pin to let routing pick one",
				ErrPinnedModelLacksVision, name)
		}
	}
	return nil
}

func visionCapCheck(req Request, base func(modelrepo.Provider) bool) func(modelrepo.Provider) bool {
	if !req.RequiresVision {
		return base
	}
	return func(p modelrepo.Provider) bool {
		return base(p) && p.CanVision()
	}
}

func classifyVisionFailure(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	base func(modelrepo.Provider) bool,
	err error,
) error {
	if !req.RequiresVision || !errors.Is(err, ErrNoSatisfactoryModel) {
		return err
	}
	textCandidates, textErr := filterCandidates(ctx, req, getModels, base)
	if textErr != nil || len(textCandidates) == 0 {
		return err
	}
	names := make([]string, 0, len(textCandidates))
	for _, p := range textCandidates {
		names = append(names, p.ModelName())
	}
	return fmt.Errorf("%w: matching models %v accept text only; use a vision-capable model for requests with images", ErrNoVisionCapableModel, names)
}

type Selection struct {
	Provider modelrepo.Provider
	Backend  string
}

func orderSelections(candidates []modelrepo.Provider, policy Policy) ([]Selection, error) {
	provider, backend, err := policy(candidates)
	if err != nil {
		return nil, err
	}
	selections := []Selection{{Provider: provider, Backend: backend}}
	skippedPick := false
	for _, p := range candidates {
		if p == nil {
			continue
		}
		for _, b := range p.GetBackendIDs() {
			if !skippedPick && b == backend && p.GetID() == provider.GetID() {
				skippedPick = true
				continue
			}
			selections = append(selections, Selection{Provider: p, Backend: b})
		}
	}
	return selections, nil
}

func chatCandidates(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
) ([]modelrepo.Provider, error) {
	if err := refusePinnedNonVision(ctx, req, getModels, modelrepo.Provider.CanChat); err != nil {
		return nil, err
	}
	if err := refusePinnedNonAudio(ctx, req, getModels, modelrepo.Provider.CanChat); err != nil {
		return nil, err
	}
	candidates, err := filterCandidates(ctx, req, getModels, audioCapCheck(req, visionCapCheck(req, modelrepo.Provider.CanChat)))
	if err != nil {
		err = classifyVisionFailure(ctx, req, getModels, modelrepo.Provider.CanChat, err)
		return nil, classifyAudioFailure(ctx, req, getModels, modelrepo.Provider.CanChat, err)
	}
	return candidates, nil
}

func ChatSelections(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	policy Policy,
) ([]Selection, error) {
	if req.ContextLength < 0 {
		return nil, fmt.Errorf("context length must be non-negative")
	}
	candidates, err := chatCandidates(ctx, req, getModels)
	if err != nil {
		return nil, err
	}
	return orderSelections(candidates, policy)
}

func Chat(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	resolver func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error),
) (modelrepo.LLMChatClient, modelrepo.Provider, string, error) {
	tracker := req.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, endFn := tracker.Start(
		ctx,
		"resolve",
		"chat_model",
		"provider_types", req.ProviderTypes,
		"model_names", req.ModelNames,
		"context_length", req.ContextLength,
		"requires_vision", req.RequiresVision,
		"requires_audio", req.RequiresAudio,
	)
	defer endFn()

	candidates, err := chatCandidates(ctx, req, getModels)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	if req.ContextLength < 0 {
		err := fmt.Errorf("context length must be non-negative")
		reportErr(err)
		return nil, nil, "", err
	}
	client, err := provider.GetChatConnection(modelrepo.WithRequestedContextLength(ctx, req.ContextLength), backend)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	reportChange("selected_provider", map[string]string{
		"model_name":  provider.ModelName(),
		"provider_id": provider.GetID(),
		"backend_id":  backend,
	})
	return client, provider, backend, nil
}

func Embed(
	ctx context.Context,
	embedReq EmbedRequest,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	resolver func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error),
) (modelrepo.LLMEmbedClient, modelrepo.Provider, string, error) {
	tracker := embedReq.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, endFn := tracker.Start(
		ctx,
		"resolve",
		"embed_model",
		"model_name", embedReq.ModelName,
		"provider_type", embedReq.ProviderType,
	)
	defer endFn()

	if embedReq.ModelName == "" {
		err := errors.New("model name is required")
		reportErr(err)
		return nil, nil, "", err
	}
	req := Request{
		ModelNames:    []string{embedReq.ModelName},
		ProviderTypes: []string{embedReq.ProviderType},
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelrepo.Provider.CanEmbed)
	if err != nil {
		reportErr(err)
		return nil, nil, "", fmt.Errorf("failed to filter candidates: %w", err)
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		reportErr(err)
		return nil, nil, "", fmt.Errorf("failed to apply resolver: %w", err)
	}
	client, err := provider.GetEmbedConnection(ctx, backend)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	reportChange("selected_provider", map[string]string{
		"model_name":  provider.ModelName(),
		"provider_id": provider.GetID(),
		"backend_id":  backend,
	})
	return client, provider, backend, nil
}

func streamCandidates(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
) ([]modelrepo.Provider, error) {
	if err := refusePinnedNonVision(ctx, req, getModels, modelrepo.Provider.CanStream); err != nil {
		return nil, err
	}
	if err := refusePinnedNonAudio(ctx, req, getModels, modelrepo.Provider.CanStream); err != nil {
		return nil, err
	}
	candidates, err := filterCandidates(ctx, req, getModels, audioCapCheck(req, visionCapCheck(req, modelrepo.Provider.CanStream)))
	if err != nil {
		err = classifyVisionFailure(ctx, req, getModels, modelrepo.Provider.CanStream, err)
		return nil, classifyAudioFailure(ctx, req, getModels, modelrepo.Provider.CanStream, err)
	}
	return candidates, nil
}

func StreamSelections(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	policy Policy,
) ([]Selection, error) {
	candidates, err := streamCandidates(ctx, req, getModels)
	if err != nil {
		return nil, err
	}
	return orderSelections(candidates, policy)
}

func Stream(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	resolver func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error),
) (modelrepo.LLMStreamClient, modelrepo.Provider, string, error) {
	tracker := req.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, endFn := tracker.Start(
		ctx,
		"resolve",
		"stream_model",
		"provider_types", req.ProviderTypes,
		"model_names", req.ModelNames,
		"context_length", req.ContextLength,
		"requires_vision", req.RequiresVision,
		"requires_audio", req.RequiresAudio,
	)
	defer endFn()

	candidates, err := streamCandidates(ctx, req, getModels)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	client, err := provider.GetStreamConnection(modelrepo.WithRequestedContextLength(ctx, req.ContextLength), backend)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	reportChange("selected_provider", map[string]string{
		"model_name":  provider.ModelName(),
		"provider_id": provider.GetID(),
		"backend_id":  backend,
	})
	return client, provider, backend, nil
}

func PromptExecuteSelections(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	policy Policy,
) ([]Selection, error) {
	if len(req.ModelNames) == 0 {
		return nil, errors.New("at least one model name is required")
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelrepo.Provider.CanPrompt)
	if err != nil {
		return nil, err
	}
	return orderSelections(candidates, policy)
}

func PromptExecute(
	ctx context.Context,
	req Request,
	getModels func(ctx context.Context, backendTypes ...string) ([]modelrepo.Provider, error),
	resolver func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error),
) (modelrepo.LLMPromptExecClient, modelrepo.Provider, string, error) {
	tracker := req.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, endFn := tracker.Start(
		ctx,
		"resolve",
		"prompt_model",
		"model_names", req.ModelNames,
		"provider_types", req.ProviderTypes,
		"context_length", req.ContextLength,
	)
	defer endFn()

	if len(req.ModelNames) == 0 {
		err := errors.New("at least one model name is required")
		reportErr(err)
		return nil, nil, "", err
	}
	candidates, err := filterCandidates(ctx, req, getModels, modelrepo.Provider.CanPrompt)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	provider, backend, err := resolver(candidates)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	client, err := provider.GetPromptConnection(modelrepo.WithRequestedContextLength(ctx, req.ContextLength), backend)
	if err != nil {
		reportErr(err)
		return nil, nil, "", err
	}
	reportChange("selected_provider", map[string]string{
		"model_name":    provider.ModelName(),
		"provider_id":   provider.GetID(),
		"provider_type": provider.GetType(),
		"backend_id":    backend,
	})
	return client, provider, backend, nil
}

type Policy = func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error)

func StickyOrRandom(sessionKey string) Policy {
	if sessionKey == "" {
		return Randomly
	}
	return func(candidates []modelrepo.Provider) (modelrepo.Provider, string, error) {
		if len(candidates) == 0 {
			return nil, "", ErrNoSatisfactoryModel
		}
		var (
			best        modelrepo.Provider
			bestBackend string
			bestScore   uint64
			found       bool
		)
		for _, p := range candidates {
			if p == nil {
				continue
			}
			for _, backend := range p.GetBackendIDs() {
				score := rendezvousScore(sessionKey, p.GetID(), backend)
				if !found || score > bestScore {
					best, bestBackend, bestScore, found = p, backend, score, true
				}
			}
		}
		if !found {
			return nil, "", ErrNoSatisfactoryModel
		}
		return best, bestBackend, nil
	}
}

func rendezvousScore(sessionKey, providerID, backendID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionKey))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(providerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(backendID))
	return h.Sum64()
}

func Randomly(candidates []modelrepo.Provider) (modelrepo.Provider, string, error) {
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

var ErrNoAvailableModels = errors.New("no models found in runtime state")

var ErrNoSatisfactoryModel = errors.New("no model matched the requirements")

var ErrNoVisionCapableModel = fmt.Errorf("%w: no available model supports image input (vision)", ErrNoSatisfactoryModel)

var ErrPinnedModelLacksVision = errors.New("explicitly requested model lacks vision capability")

func selectRandomBackend(provider modelrepo.Provider) (string, error) {
	if provider == nil {
		return "", ErrNoSatisfactoryModel
	}

	backendIDs := provider.GetBackendIDs()
	if len(backendIDs) == 0 {
		return "", ErrNoSatisfactoryModel
	}

	return backendIDs[rand.Intn(len(backendIDs))], nil
}

func selectRandomProvider(candidates []modelrepo.Provider) (modelrepo.Provider, error) {
	if len(candidates) == 0 {
		return nil, ErrNoSatisfactoryModel
	}

	return candidates[rand.Intn(len(candidates))], nil
}
