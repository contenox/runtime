package llmrepo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/internal/models/ollamatokenizer"
	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
)

var _ ModelRepo = (*modelManager)(nil)

type Request struct {
	WorkerRole        string
	ProviderTypes     []string
	ModelNames        []string
	ContextLength     int
	FitContextToModel bool
	SessionKey        string
	CacheHints        *CacheHints
	Tracker           libtracker.ActivityTracker
	BackendHealth     BackendHealth
}

func (e *modelManager) backendAllowedByHealth(ctx context.Context, req Request, endpoint string) bool {
	if req.BackendHealth == nil || endpoint == "" {
		return true
	}
	return req.BackendHealth(e.backendIDForEndpoint(ctx, endpoint))
}

func (e *modelManager) healthyResolver(ctx context.Context, health BackendHealth) func([]libmodelprovider.Provider) (libmodelprovider.Provider, string, error) {
	if health == nil {
		return llmresolver.Randomly
	}
	allowed := func(endpoint string) bool {
		return health(e.backendIDForEndpoint(ctx, endpoint))
	}
	return func(candidates []libmodelprovider.Provider) (libmodelprovider.Provider, string, error) {
		healthy := make([]libmodelprovider.Provider, 0, len(candidates))
		for _, p := range candidates {
			endpoint := ""
			if ids := p.GetBackendIDs(); len(ids) > 0 {
				endpoint = ids[0]
			}
			if allowed(endpoint) {
				healthy = append(healthy, p)
			}
		}
		if len(healthy) == 0 {
			return nil, "", fmt.Errorf("embedding: no candidate backend is in service: %w", llmresolver.ErrNoSatisfactoryModel)
		}
		return llmresolver.Randomly(healthy)
	}
}

func (e *modelManager) backendIDForEndpoint(ctx context.Context, endpoint string) string {
	if e.runtime == nil {
		return endpoint
	}
	for _, state := range e.runtime.Get(ctx) {
		if state.Backend.BaseURL == endpoint || state.ID == endpoint || state.Backend.ID == endpoint {
			if state.Backend.ID != "" {
				return state.Backend.ID
			}
			return state.ID
		}
	}
	return endpoint
}

type EmbedRequest struct {
	ModelName     string
	ProviderType  string
	Tracker       libtracker.ActivityTracker
	BackendHealth BackendHealth
}

type Meta struct {
	ModelName    string                       `json:"model_name"`
	ProviderType string                       `json:"provider_type"`
	BackendID    string                       `json:"backend_id"`
	Usage        *libmodelprovider.TokenUsage `json:"usage,omitempty"`
}

type ModelRepo interface {
	Autocomplete(context.Context, Request, libmodelprovider.AutocompleteRequest) (libmodelprovider.AutocompleteResult, Meta, error)
	NextEdit(context.Context, Request, libmodelprovider.NextEditRequest) (libmodelprovider.NextEditResult, Meta, error)
	Tokenize(ctx context.Context, modelName string, prompt string) ([]int, error)
	CountTokens(ctx context.Context, modelName string, prompt string) (int, error)
	PromptExecute(
		ctx context.Context,
		req Request,
		systeminstruction string, temperature float32, prompt string,
		args ...libmodelprovider.PromptArgument,
	) (string, Meta, error)
	Chat(
		ctx context.Context,
		req Request,
		Messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument,
	) (libmodelprovider.ChatResult, Meta, error)
	Embed(
		ctx context.Context,
		embedReq EmbedRequest,
		prompt string,
	) ([]float64, Meta, error)
	Stream(
		ctx context.Context,
		req Request,
		messages []libmodelprovider.Message,
		opts ...libmodelprovider.ChatArgument,
	) (<-chan *libmodelprovider.StreamParcel, Meta, error)
}

type Tokenizer interface {
	Tokenize(ctx context.Context, prompt string) ([]int, error)
	CountTokens(ctx context.Context, prompt string) (int, error)
}

var _ ModelRepo = (*modelManager)(nil)

type modelManager struct {
	runtime   *runtimestate.State
	tokenizer ollamatokenizer.Tokenizer
	config    ModelManagerConfig
	mu        sync.RWMutex
	tracker   libtracker.ActivityTracker

	reconcileMu sync.Mutex
	refreshes   map[string]*resolutionRefresh
}

const minResolveReconcileInterval = 5 * time.Second

type resolutionRefresh struct {
	done chan struct{}
	at   time.Time
	err  error
}

func (e *modelManager) reconcileForResolution(ctx context.Context, resolveErr error, providers ...string) bool {
	if !errors.Is(resolveErr, llmresolver.ErrNoAvailableModels) && !errors.Is(resolveErr, llmresolver.ErrNoSatisfactoryModel) {
		return false
	}
	keys := make([]string, 0, len(providers))
	for _, provider := range providers {
		if key := libmodelprovider.CanonicalBackendType(provider); key != "" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	key := strings.Join(slices.Compact(keys), "\x00")
	e.reconcileMu.Lock()
	if refresh := e.refreshes[key]; refresh != nil && (refresh.at.IsZero() || (refresh.err == nil && time.Since(refresh.at) < minResolveReconcileInterval)) {
		e.reconcileMu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-refresh.done:
			return refresh.err == nil
		}
	}
	refresh := &resolutionRefresh{done: make(chan struct{})}
	if e.refreshes == nil {
		e.refreshes = make(map[string]*resolutionRefresh)
	}
	e.refreshes[key] = refresh
	e.reconcileMu.Unlock()
	err := e.runtime.RunProviderCycle(ctx, providers...)
	e.reconcileMu.Lock()
	refresh.err, refresh.at = err, time.Now()
	close(refresh.done)
	e.reconcileMu.Unlock()
	if err != nil && e.tracker != nil {
		reportErr, _, end := e.tracker.Start(ctx, "reconcile", "model_resolution")
		reportErr(err)
		end()
	}
	return err == nil
}

type ModelConfig struct {
	Name     string
	Provider string
}

type ModelManagerConfig struct {
	EmbeddingDefault      func(context.Context) (ModelConfig, error)
	DefaultPromptModel    ModelConfig
	DefaultEmbeddingModel ModelConfig
	DefaultChatModel      ModelConfig
	DefaultAudioModel     ModelConfig
}

func (e *modelManager) applyAudioDefault(req *Request, messages []libmodelprovider.Message) {
	if !libmodelprovider.MessagesHaveAudio(messages) {
		return
	}
	if len(req.ModelNames) == 0 && e.config.DefaultAudioModel.Name != "" {
		req.ModelNames = []string{e.config.DefaultAudioModel.Name}
		if len(req.ProviderTypes) == 0 && e.config.DefaultAudioModel.Provider != "" {
			req.ProviderTypes = []string{e.config.DefaultAudioModel.Provider}
		}
	}
}

func NewModelManager(runtime *runtimestate.State, tokenizer ollamatokenizer.Tokenizer, config ModelManagerConfig, tracker libtracker.ActivityTracker) (*modelManager, error) {
	if runtime == nil {
		return nil, errors.New("runtime cannot be nil")
	}
	if tokenizer == nil {
		return nil, errors.New("tokenizer cannot be nil")
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	return &modelManager{
		runtime:   runtime,
		tokenizer: tokenizer,
		config:    config,
		tracker:   tracker,
	}, nil
}

func (e *modelManager) Tokenize(ctx context.Context, modelName string, prompt string) ([]int, error) {
	if prompt == "" {
		return []int{}, nil
	}

	tokenizer, err := e.GetTokenizer(ctx, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get tokenizer: %w", err)
	}

	tokens, err := tokenizer.Tokenize(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("tokenization failed: %w", err)
	}

	return tokens, nil
}

func (e *modelManager) CountTokens(ctx context.Context, modelName string, prompt string) (int, error) {
	if prompt == "" {
		return 0, nil
	}

	tokenizer, err := e.GetTokenizer(ctx, modelName)
	if err != nil {
		return 0, fmt.Errorf("failed to get tokenizer: %w", err)
	}

	count, err := tokenizer.CountTokens(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("token counting failed: %w", err)
	}

	return count, nil
}

func (e *modelManager) PromptExecute(
	ctx context.Context,
	req Request,
	systemInstruction string, temperature float32, prompt string,
	args ...libmodelprovider.PromptArgument,
) (string, Meta, error) {
	if req.WorkerRole != "" {
	}
	if err := validateRequest(req); err != nil {
		return "", Meta{}, fmt.Errorf("invalid request: %w", err)
	}
	if _, err := libmodelprovider.PromptConfigFromArgs(args...); err != nil {
		return "", Meta{}, fmt.Errorf("invalid prompt settings: %w", err)
	}

	runtimeStateResolution := e.GetRuntime(ctx)

	if len(req.ModelNames) == 0 {
		req.ModelNames = []string{e.config.DefaultPromptModel.Name}
	}
	if len(req.ProviderTypes) == 0 {
		req.ProviderTypes = []string{e.config.DefaultPromptModel.Provider}
	}

	resolverReq := e.convertToResolverRequest(req, nil)
	policy := llmresolver.StickyOrRandom(effectiveSessionKey(ctx, req))
	selections, err := llmresolver.PromptExecuteSelections(ctx,
		resolverReq,
		runtimeStateResolution,
		policy,
	)
	if err != nil && e.reconcileForResolution(ctx, err, req.ProviderTypes...) {
		selections, err = llmresolver.PromptExecuteSelections(ctx,
			resolverReq,
			e.GetRuntime(ctx),
			policy,
		)
	}
	if err != nil {
		return "", Meta{}, fmt.Errorf("prompt execute: client resolution failed: %w", err)
	}

	return e.runPromptSelections(ctx, req, selections, systemInstruction, temperature, prompt, args)
}

func (e *modelManager) Chat(
	ctx context.Context,
	req Request,
	messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument,
) (libmodelprovider.ChatResult, Meta, error) {
	if req.WorkerRole != "" {
	}
	if err := validateRequest(req); err != nil {
		return libmodelprovider.ChatResult{}, Meta{}, fmt.Errorf("invalid request: %w", err)
	}

	if len(messages) == 0 {
		return libmodelprovider.ChatResult{}, Meta{}, fmt.Errorf("%w: messages cannot be empty", ErrInvalidRequest)
	}

	runtimeStateResolution := e.GetRuntime(ctx)

	e.applyAudioDefault(&req, messages)
	if len(req.ModelNames) == 0 {
		req.ModelNames = []string{e.config.DefaultChatModel.Name}
	}
	if len(req.ProviderTypes) == 0 {
		req.ProviderTypes = []string{e.config.DefaultChatModel.Provider}
	}

	resolverReq := e.convertToResolverRequest(req, messages)
	sessionKey := effectiveSessionKey(ctx, req)
	policy := llmresolver.StickyOrRandom(sessionKey)
	selections, err := llmresolver.ChatSelections(ctx,
		resolverReq,
		runtimeStateResolution,
		policy,
	)
	if err != nil && e.reconcileForResolution(ctx, err, req.ProviderTypes...) {
		selections, err = llmresolver.ChatSelections(ctx,
			resolverReq,
			e.GetRuntime(ctx),
			policy,
		)
	}
	if err != nil {
		return libmodelprovider.ChatResult{}, Meta{}, fmt.Errorf("chat: client resolution failed: %w", err)
	}

	return e.runChatSelections(ctx, req, selections, sessionKey, messages, opts)
}

func (e *modelManager) Embed(
	ctx context.Context,
	embedReq EmbedRequest,
	prompt string,
) ([]float64, Meta, error) {
	if prompt == "" {
		return nil, Meta{}, fmt.Errorf("%w: prompt cannot be empty", ErrInvalidRequest)
	}

	runtimeStateResolution := e.GetRuntime(ctx)

	if embedReq.ModelName == "" {
		selected := e.config.DefaultEmbeddingModel
		if e.config.EmbeddingDefault != nil {
			var err error
			selected, err = e.config.EmbeddingDefault(ctx)
			if err != nil {
				return nil, Meta{}, err
			}
		}
		embedReq.ModelName = selected.Name
		if embedReq.ProviderType == "" {
			embedReq.ProviderType = selected.Provider
		}
	}
	if embedReq.ProviderType == "" {
		embedReq.ProviderType = e.config.DefaultEmbeddingModel.Provider
	}

	resolverReq := e.convertToResolverEmbedRequest(embedReq)
	client, provider, backend, err := llmresolver.Embed(ctx,
		resolverReq,
		runtimeStateResolution,
		e.healthyResolver(ctx, embedReq.BackendHealth),
	)
	if err != nil && e.reconcileForResolution(ctx, err, embedReq.ProviderType) {
		client, provider, backend, err = llmresolver.Embed(ctx,
			resolverReq,
			e.GetRuntime(ctx),
			e.healthyResolver(ctx, embedReq.BackendHealth),
		)
	}
	if err != nil {
		return nil, Meta{}, fmt.Errorf("embed: client resolution failed: %w", err)
	}
	defer safeClose(client)

	meta := Meta{
		ModelName:    provider.ModelName(),
		ProviderType: provider.GetType(),
		BackendID:    backend,
	}

	if err := e.enforceResolutionBounds(ctx, "embed", provider, backend); err != nil {
		return nil, meta, err
	}

	embeddings, err := client.Embed(ctx, prompt)
	if err != nil {
		return nil, meta, fmt.Errorf("embedding generation failed: %w", err)
	}
	return embeddings, meta, nil
}

func (e *modelManager) Stream(
	ctx context.Context,
	req Request,
	messages []libmodelprovider.Message,
	opts ...libmodelprovider.ChatArgument,
) (<-chan *libmodelprovider.StreamParcel, Meta, error) {
	if req.WorkerRole != "" {
	}
	if len(messages) == 0 {
		return nil, Meta{}, fmt.Errorf("%w: messages cannot be empty", ErrInvalidRequest)
	}

	if err := validateRequest(req); err != nil {
		return nil, Meta{}, fmt.Errorf("invalid request: %w", err)
	}

	runtimeStateResolution := e.GetRuntime(ctx)

	e.applyAudioDefault(&req, messages)
	if len(req.ModelNames) == 0 && e.config.DefaultChatModel.Name != "" {
		req.ModelNames = []string{e.config.DefaultChatModel.Name}
	}
	if len(req.ProviderTypes) == 0 && e.config.DefaultChatModel.Provider != "" {
		req.ProviderTypes = []string{e.config.DefaultChatModel.Provider}
	}

	resolverReq := e.convertToResolverRequest(req, messages)
	sessionKey := effectiveSessionKey(ctx, req)
	policy := llmresolver.StickyOrRandom(sessionKey)
	selections, err := llmresolver.StreamSelections(ctx,
		resolverReq,
		runtimeStateResolution,
		policy,
	)
	if err != nil && e.reconcileForResolution(ctx, err, req.ProviderTypes...) {
		selections, err = llmresolver.StreamSelections(ctx,
			resolverReq,
			e.GetRuntime(ctx),
			policy,
		)
	}
	if err != nil {
		return nil, Meta{}, fmt.Errorf("stream: client resolution failed: %w", err)
	}

	return e.runStreamSelections(ctx, req, selections, sessionKey, messages, opts)
}

func mergeTokenUsage(dst *libmodelprovider.TokenUsage, src *libmodelprovider.TokenUsage) {
	dst.Merge(src)
}

func (e *modelManager) reportTokenUsage(ctx context.Context, req Request, meta Meta, usage libmodelprovider.TokenUsage) {
	tracker := req.Tracker
	if tracker == nil {
		tracker = e.tracker
	}
	if tracker == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	_, reportChange, end := tracker.Start(ctx, "report", "token_usage",
		"model", meta.ModelName,
		"provider_type", meta.ProviderType,
		"backend_id", meta.BackendID,
	)
	defer end()
	reportChange("token_usage", map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"cache_read":        usage.CacheReadTokens,
		"cache_write":       usage.CacheWriteTokens,
	})
}

func (e *modelManager) GetRuntime(ctx context.Context) runtimestate.ProviderFromRuntimeState {
	return e.runtime.ExecutionProviders(e.tracker)
}

func (e *modelManager) GetTokenizer(ctx context.Context, modelName string) (Tokenizer, error) {
	if e.tokenizer == nil {
		return nil, errors.New("tokenizer not initialized")
	}

	modelForTokenization, err := e.tokenizer.OptimalModel(ctx, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get optimal tokenizer model: %w", err)
	}

	return &tokenizerAdapter{
		tokenizer: e.tokenizer,
		modelName: modelForTokenization,
	}, nil
}

func (e *modelManager) convertToResolverRequest(req Request, messages []libmodelprovider.Message) llmresolver.Request {
	return llmresolver.Request{
		ProviderTypes:     req.ProviderTypes,
		ModelNames:        req.ModelNames,
		ContextLength:     req.ContextLength,
		FitContextToModel: req.FitContextToModel,
		RequiresVision:    libmodelprovider.MessagesHaveImages(messages),
		RequiresAudio:     libmodelprovider.MessagesHaveAudio(messages),
		Tracker:           req.Tracker,
	}
}

func (e *modelManager) convertToResolverEmbedRequest(req EmbedRequest) llmresolver.EmbedRequest {
	return llmresolver.EmbedRequest{
		ModelName:    req.ModelName,
		ProviderType: req.ProviderType,
		Tracker:      req.Tracker,
	}
}

func validateRequest(req Request) error {
	if req.ContextLength < 0 {
		return fmt.Errorf("%w: context length must be non-negative", ErrInvalidRequest)
	}
	return nil
}

func safeClose(closer interface{}) {
	if closer == nil {
		return
	}

	switch c := closer.(type) {
	case interface{ Close() error }:
		_ = c.Close()
	case interface{ Close() }:
		c.Close()
	}
}

type tokenizerAdapter struct {
	tokenizer ollamatokenizer.Tokenizer
	modelName string
}

func (a *tokenizerAdapter) Tokenize(ctx context.Context, prompt string) ([]int, error) {
	return a.tokenizer.Tokenize(ctx, a.modelName, prompt)
}

func (a *tokenizerAdapter) CountTokens(ctx context.Context, prompt string) (int, error) {
	return a.tokenizer.CountTokens(ctx, a.modelName, prompt)
}
