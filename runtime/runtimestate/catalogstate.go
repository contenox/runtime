package runtimestate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/libcipher"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelcapability"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const observedModelCachePrefix = "prov:"

func observedModelCacheKey(backendID string) string {
	return observedModelCachePrefix + backendID
}

func (s *State) RefreshProviderModels(ctx context.Context, providers ...string) error {
	backends, err := runtimetypes.New(s.dbInstance.WithoutTransaction()).ListBackends(ctx, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return err
	}
	for _, backend := range backends {
		if !matchesProvider(backend, providers) {
			continue
		}
		if s.kvStore != nil {
			if err := InvalidateModelCache(ctx, s.kvStore, backend.ID); err != nil {
				return err
			}
		}
		s.providerCache.Delete(backend.ID)
	}
	return s.RunProviderCycle(ctx, providers...)
}

func InvalidateModelCache(ctx context.Context, kv libkvstore.KVManager, backendID string) error {
	if kv == nil {
		return nil
	}
	exec, err := kv.Executor(ctx)
	if err != nil {
		return err
	}
	return exec.Delete(ctx, observedModelCacheKey(backendID))
}

func ClearModelCache(ctx context.Context, kv libkvstore.KVManager) (int, error) {
	if kv == nil {
		return 0, nil
	}
	exec, err := kv.Executor(ctx)
	if err != nil {
		return 0, err
	}
	keys, err := exec.Keys(ctx, observedModelCachePrefix+"*")
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, k := range keys {
		if err := exec.Delete(ctx, k); err != nil {
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
}

func takesCredential(backendType string) bool {
	switch modelrepo.CanonicalBackendType(backendType) {
	case "ollama", "openai", "anthropic", "mistral", "deepseek", "bedrock", "gemini", "vllm", "vertex-google":
		return true
	}
	return false
}

func BackendCredentialKey(backendType, backendID string) string {
	return ProviderKeyPrefix + strings.ToLower(backendType) + ":" + backendID
}

func (s *State) loadProviderAPIKey(ctx context.Context, backend *runtimetypes.Backend) (string, error) {
	return loadProviderAPIKey(ctx, runtimetypes.New(s.dbInstance.WithoutTransaction()), backend)
}

func LoadProviderAPIKey(ctx context.Context, db libdbexec.DBManager, backend runtimetypes.Backend) (string, error) {
	return loadProviderAPIKey(ctx, runtimetypes.New(db.WithoutTransaction()), &backend)
}

func loadProviderAPIKey(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend) (string, error) {
	if !takesCredential(backend.Type) {
		return "", nil
	}
	var record ProviderConfig
	err := store.GetKV(ctx, BackendCredentialKey(backend.Type, backend.ID), &record)
	if errors.Is(err, libdbexec.ErrNotFound) && optionalCredential(backend.Type) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	credential, err := record.Credential(backend.Type)
	var unset *EnvironmentUnsetError
	switch {
	case errors.As(err, &unset):
		return "", fmt.Errorf("%w; connection %q takes its API key from it: set it for this runtime or save the connection with another credential source", unset, backend.Name)
	case err != nil:
		return "", fmt.Errorf("connection %q: %w", backend.Name, err)
	}
	return credential, nil
}

func optionalCredential(backendType string) bool {
	switch modelrepo.CanonicalBackendType(backendType) {
	case "ollama", "local", "modeld", "vllm":
		return true
	}
	return false
}

func (s *State) newCatalogProvider(backend *runtimetypes.Backend, apiKey string) (modelrepo.CatalogProvider, error) {
	return modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{
			Type:    backend.Type,
			BaseURL: backend.BaseURL,
			APIKey:  apiKey,
		},
		modelrepo.WithCatalogHTTPClient(modelrepo.SharedHTTPClient),
	)
}

type storedCacheEntry struct {
	providerCacheEntry
	APIKey *string `json:"api_key,omitempty"`
}

func (s *State) credentialHash(backendID, credential string) (string, error) {
	sum, err := libcipher.NewHash(libcipher.GenerateHashArgs{Payload: []byte(credential), SigningKey: s.cacheHashKey, Salt: []byte(backendID)}, sha256.New)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum), nil
}

func (s *State) reportCacheFailure(ctx context.Context, backendID, operation string, err error) {
	tracker := s.tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, _, end := tracker.Start(ctx, operation, "model_cache", "backend", backendID)
	reportErr(err)
	end()
}

func (s *State) loadObservedModelCache(ctx context.Context, backendID, credential string) ([]modelrepo.ObservedModel, bool) {
	hash, err := s.credentialHash(backendID, credential)
	if err != nil {
		s.reportCacheFailure(ctx, backendID, "read", err)
		return nil, false
	}
	if s.kvStore == nil {
		if cached, ok := s.providerCache.Load(backendID); ok {
			if entry, ok := cached.(providerCacheEntry); ok && hmac.Equal([]byte(entry.CredentialHash), []byte(hash)) && len(entry.Models) > 0 {
				return entry.Models, true
			}
		}
		return nil, false
	}
	exec, err := s.kvStore.Executor(ctx)
	if err != nil {
		s.reportCacheFailure(ctx, backendID, "read", err)
		return nil, false
	}
	key := observedModelCacheKey(backendID)
	raw, err := exec.Get(ctx, key)
	if err != nil {
		if !errors.Is(err, libkvstore.ErrNotFound) {
			s.reportCacheFailure(ctx, backendID, "read", err)
		}
		return nil, false
	}
	var entry storedCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		s.reportCacheFailure(ctx, backendID, "read", err)
		return nil, false
	}
	if entry.APIKey != nil {
		if err := exec.Delete(ctx, key); err != nil {
			s.reportCacheFailure(ctx, backendID, "drop_plaintext", err)
		}
		return nil, false
	}
	if !hmac.Equal([]byte(entry.CredentialHash), []byte(hash)) || len(entry.Models) == 0 {
		return nil, false
	}
	return entry.Models, true
}

func (s *State) storeObservedModelCache(ctx context.Context, backendID, credential string, models []modelrepo.ObservedModel, catalog modelrepo.CatalogProvider) {
	policy := modelrepo.ModelCachePolicy{}
	if p, ok := catalog.(modelrepo.CachePolicyCatalog); ok {
		policy = p.CachePolicy()
	}
	ttl := ProviderCacheDuration
	if len(models) == 0 {
		if policy.Empty < 0 {
			return
		}
		if policy.Empty > 0 {
			ttl = policy.Empty
		}
	} else if policy.Healthy > 0 {
		ttl = policy.Healthy
	}
	hash, err := s.credentialHash(backendID, credential)
	if err != nil {
		s.reportCacheFailure(ctx, backendID, "write", err)
		return
	}
	entry := providerCacheEntry{Models: models, CredentialHash: hash}
	if s.kvStore == nil {
		s.providerCache.Store(backendID, entry)
		return
	}
	exec, err := s.kvStore.Executor(ctx)
	if err != nil {
		s.reportCacheFailure(ctx, backendID, "write", err)
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		s.reportCacheFailure(ctx, backendID, "write", err)
		return
	}
	if err := exec.SetWithTTL(ctx, observedModelCacheKey(backendID), data, ttl); err != nil {
		s.reportCacheFailure(ctx, backendID, "write", err)
	}
}

func observedModelNames(models []modelrepo.ObservedModel) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	return names
}

func (s *State) applyDeclaredModel(ctx context.Context, backend *runtimetypes.Backend, declared *runtimetypes.Model, observed modelrepo.ObservedModel) ModelPullStatus {
	model := pullStatusFromObservedModel(observed)
	if declared != nil {
		model = s.mergeDeclaredRow(ctx, declared, model)
	}
	return s.applyDeclarations(ctx, backend, model)
}

func (s *State) mergeDeclaredRow(ctx context.Context, declared *runtimetypes.Model, model ModelPullStatus) ModelPullStatus {
	if declared.ContextLength == 0 && model.ContextLength > 0 {
		learned := *declared
		learned.ContextLength = model.ContextLength
		learned.CanChat = model.CanChat
		learned.CanEmbed = model.CanEmbed
		learned.CanPrompt = model.CanPrompt
		learned.CanStream = model.CanStream
		_ = runtimetypes.New(s.dbInstance.WithoutTransaction()).UpdateModel(ctx, &learned)
	}
	if declared.ContextLength > 0 {
		model.ContextLength = declared.ContextLength
	}
	model.CanChat = model.CanChat || declared.CanChat
	model.CanPrompt = model.CanPrompt || declared.CanPrompt
	model.CanStream = model.CanStream || declared.CanStream
	return model
}

func (s *State) applyDeclarations(ctx context.Context, backend *runtimetypes.Backend, model ModelPullStatus) ModelPullStatus {
	model = s.applyCapabilityOverrides(ctx, backend.Type, model)
	model = s.applyCapabilityOverrides(ctx, ConnectionSelector(backend.ID), model)
	return s.applyModelFacts(ctx, backend.ID, model)
}

func (s *State) applyCapabilityOverrides(ctx context.Context, provider string, model ModelPullStatus) ModelPullStatus {
	provider = modelrepo.CanonicalBackendType(provider)
	name := declaredModelName(model)
	if name == "" {
		return model
	}
	override, ok, err := modelcapability.New(runtimetypes.New(s.dbInstance.WithoutTransaction())).Get(ctx, provider, name)
	if err != nil || !ok {
		return model
	}
	if override.CanThink != nil {
		model.CanThink = *override.CanThink
	}
	if override.CanVision != nil {
		model.CanVision = *override.CanVision
	}
	if override.CompletionMode != nil {
		model.CompletionMode = *override.CompletionMode
	}
	return model
}

func (s *State) applyModelFacts(ctx context.Context, providerID string, model ModelPullStatus) ModelPullStatus {
	name := declaredModelName(model)
	if providerID == "" || name == "" {
		return model
	}
	declared, err := runtimetypes.New(s.dbInstance.WithoutTransaction()).GetLLMProviderModelFacts(ctx, providerID)
	if err != nil {
		return model
	}
	facts, ok := declared[name]
	if !ok || facts.IsZero() {
		return model
	}
	if facts.ContextLength > 0 {
		model.ContextLength = facts.ContextLength
	}
	if facts.MaxOutputTokens > 0 {
		model.MaxOutputTokens = facts.MaxOutputTokens
	}
	if facts.CompletionMode != "" {
		model.CompletionMode, model.FIMFormat = facts.CompletionMode, facts.FIMFormat
	}
	if len(facts.Capabilities) > 0 {
		model.NextEditFormat = facts.NextEditFormat
	}
	if len(facts.Capabilities) > 0 {
		model.DeclaredCapabilities = facts.Capabilities
		applyDeclaredCapabilities(&model, facts.Capabilities)
	}
	if facts.Pricing != nil {
		model.Pricing = facts.Pricing
	}
	return model
}

func declaredModelName(model ModelPullStatus) string {
	if name := strings.TrimSpace(model.Model); name != "" {
		return name
	}
	return strings.TrimSpace(model.Name)
}

func applyDeclaredCapabilities(model *ModelPullStatus, capabilities []string) {
	model.CanAutocomplete = false
	model.CanNextEdit = false
	model.CanChat = false
	model.CanEmbed = false
	model.CanPrompt = false
	model.CanStream = false
	model.CanThink = false
	model.CanVision = false
	model.CanAudio = false
	for _, capability := range capabilities {
		switch capability {
		case runtimetypes.CapabilityAutocomplete:
			model.CanAutocomplete = true
		case runtimetypes.CapabilityNextEdit:
			model.CanNextEdit = true
		case runtimetypes.CapabilityCompletion:
			model.CanChat = true
			model.CanPrompt = true
			model.CanStream = true
		case runtimetypes.CapabilityEmbedding:
			model.CanEmbed = true
		case runtimetypes.CapabilityTools:
			model.CanChat = true
		case runtimetypes.CapabilityVision:
			model.CanVision = true
		case runtimetypes.CapabilityThinking:
			model.CanThink = true
		case runtimetypes.CapabilityAudio:
			model.CanAudio = true
		}
	}
}

func storeBackendError(state *State, backend *runtimetypes.Backend, apiKey string, err error, models []string) {
	runtimeState := &BackendRuntimeState{
		ID:           backend.ID,
		Name:         backend.Name,
		Models:       models,
		PulledModels: []ModelPullStatus{},
		Backend:      *backend,
	}
	if err != nil {
		runtimeState.Error = err.Error()
	}
	runtimeState.SetAPIKey(apiKey)
	state.state.Store(backend.ID, runtimeState)
}

func declaredModelDebugMap(declaredModels map[string]*runtimetypes.Model) []string {
	declaredMap := make([]string, 0, len(declaredModels))
	for key, model := range declaredModels {
		payload := "model-data==nil"
		if model != nil {
			payload = model.ID + " " + model.Model
		}
		declaredMap = append(declaredMap, key+":"+payload)
	}
	return declaredMap
}

func declaredModelsUnavailableError(provider string, declaredModels map[string]*runtimetypes.Model, available []string) error {
	return fmt.Errorf(
		"None of the declared models are available in the %s API: declared models: %v \navailable models %s",
		provider,
		strings.Join(declaredModelDebugMap(declaredModels), ", "),
		available,
	)
}

const OpenaiKey = ProviderKeyPrefix + "openai"
