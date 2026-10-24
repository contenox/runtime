package runtimestate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libcipher"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const ProviderCacheDuration = 1 * time.Hour

var ReconcileDebounceInterval = 15 * time.Second

type providerCacheEntry struct {
	Models         []modelrepo.ObservedModel `json:"models"`
	CredentialHash string                    `json:"credential_hash"`
}

type State struct {
	dbInstance         libdb.DBManager
	state              sync.Map
	psInstance         libbus.Messenger
	withgroups         bool
	autoDiscoverModels bool
	kvStore            libkvstore.KVManager
	providerCache      sync.Map
	cacheHashKey       []byte
	reconcileMu        sync.Mutex
	lastReconcileAt    time.Time
	tracker            libtracker.ActivityTracker
	backendMu          sync.Mutex
	backendRuns        map[string]*backendRun
	discovery          *discoveryLifecycle
	observersMu        sync.Mutex
	observers          map[int]func()
	nextObserver       int
}

type Option func(*State)

func WithGroups() Option {
	return func(s *State) {
		s.withgroups = true
	}
}

func WithTracker(tracker libtracker.ActivityTracker) Option {
	return func(s *State) {
		s.tracker = tracker
	}
}

func WithKVStore(kv libkvstore.KVManager) Option {
	return func(s *State) {
		s.kvStore = kv
	}
}

func WithSkipDeleteUndeclaredModels() Option {
	return func(s *State) {}
}

func WithAutoDiscoverModels() Option {
	return func(s *State) {
		s.autoDiscoverModels = true
	}
}

func New(ctx context.Context, dbInstance libdb.DBManager, psInstance libbus.Messenger, options ...Option) (*State, error) {
	s := &State{
		dbInstance: dbInstance,
		state:      sync.Map{},
		psInstance: psInstance,
	}
	if psInstance == nil {
		return nil, errors.New("psInstance cannot be nil")
	}
	if dbInstance == nil {
		return nil, errors.New("dbInstance cannot be nil")
	}
	for _, option := range options {
		option(s)
	}
	hashKey, err := libcipher.GenerateKey(32)
	if err != nil {
		return nil, fmt.Errorf("model cache hash key: %w", err)
	}
	s.cacheHashKey = []byte(hashKey)
	store := runtimetypes.New(dbInstance.WithoutTransaction())
	if err := AdoptSharedCredentialRows(ctx, store); err != nil {
		return nil, err
	}
	backends, err := store.ListAllBackends(ctx)
	if err != nil {
		return nil, err
	}
	for _, backend := range backends {
		if err := s.ReconcileDeclarations(ctx, backend.ID); err != nil {
			storeBackendError(s, backend, "", err, nil)
		}
	}
	return s, nil
}

func (s *State) RunBackendCycle(ctx context.Context) error {
	return s.runBackendCycle(ctx, nil)
}

func (s *State) RunProviderCycle(ctx context.Context, providers ...string) error {
	return s.runBackendCycle(ctx, providers)
}

func (s *State) runBackendCycle(ctx context.Context, providers []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before := s.signature()
	defer s.notifyIfChanged(before)
	var err error
	if s.withgroups {
		err = s.syncBackendsWithgroups(ctx, providers)
	} else {
		err = s.syncBackends(ctx, providers)
	}
	if matchesEveryProvider(providers) {
		s.markReconciled(time.Now())
	}
	return err
}

func (s *State) ReconcileIfStale(ctx context.Context) error {
	if s.discovery != nil {
		if err := s.scheduleUnobserved(ctx); err != nil {
			return err
		}
		return s.discovery.refresh(ctx, s)
	}
	if !s.claimReconcile(time.Now()) {
		return nil
	}
	return s.RunBackendCycle(ctx)
}

func (s *State) claimReconcile(now time.Time) bool {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	if !s.lastReconcileAt.IsZero() && now.Sub(s.lastReconcileAt) < ReconcileDebounceInterval {
		return false
	}
	s.lastReconcileAt = now
	return true
}

func (s *State) markReconciled(now time.Time) {
	s.reconcileMu.Lock()
	s.lastReconcileAt = now
	s.reconcileMu.Unlock()
}

func (s *State) Observe(fn func()) (func(), error) {
	if fn == nil {
		return nil, errors.New("discovery observer is required")
	}
	s.observersMu.Lock()
	defer s.observersMu.Unlock()
	if s.observers == nil {
		s.observers = map[int]func(){}
	}
	id := s.nextObserver
	s.nextObserver++
	s.observers[id] = fn
	return func() {
		s.observersMu.Lock()
		delete(s.observers, id)
		s.observersMu.Unlock()
	}, nil
}

func (s *State) notifyIfChanged(before string) {
	if s.signature() == before {
		return
	}
	s.observersMu.Lock()
	observers := make([]func(), 0, len(s.observers))
	for _, fn := range s.observers {
		observers = append(observers, fn)
	}
	s.observersMu.Unlock()
	for _, fn := range observers {
		fn()
	}
}

func (s *State) signature() string {
	var parts []string
	s.state.Range(func(_, value any) bool {
		backend, ok := value.(*BackendRuntimeState)
		if !ok {
			return true
		}
		raw, err := json.Marshal(backend)
		if err != nil {
			parts = append(parts, backend.ID+"\x1f"+err.Error())
			return true
		}
		parts = append(parts, string(raw))
		return true
	})
	sort.Strings(parts)
	return strings.Join(parts, "\x1e")
}

func (s *State) Get(ctx context.Context) map[string]BackendRuntimeState {
	state := map[string]BackendRuntimeState{}
	s.state.Range(func(key, value any) bool {
		backend, ok := value.(*BackendRuntimeState)
		if !ok {
			return true
		}
		var backendCopy BackendRuntimeState
		raw, err := json.Marshal(backend)
		if err != nil {
			s.reportCopyFailure(ctx, backend, err)
			return true
		}
		if err := json.Unmarshal(raw, &backendCopy); err != nil {
			s.reportCopyFailure(ctx, backend, err)
			return true
		}
		backendCopy.SetAPIKey(backend.GetAPIKey())
		backendCopy.authorize = backend.authorize
		state[backend.ID] = backendCopy
		return true
	})
	return state
}

func (s *State) reportCopyFailure(ctx context.Context, backend *BackendRuntimeState, err error) {
	tracker := s.tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, _, end := tracker.Start(ctx, "snapshot", "state_copy", "backend", backend.ID)
	reportErr(err)
	end()
}

func (s *State) syncBackendsWithgroups(ctx context.Context, providers []string) error {
	tx := s.dbInstance.WithoutTransaction()
	dbStore := runtimetypes.New(tx)

	allgroups, err := dbStore.ListAllAffinityGroups(ctx)
	if err != nil {
		return fmt.Errorf("fetching groups: %v", err)
	}

	allBackendObjects := make(map[string]*runtimetypes.Backend)
	backendToAggregatedModels := make(map[string]map[string]*runtimetypes.Model)
	activeBackendIDs := make(map[string]struct{})

	for _, group := range allgroups {
		groupBackends, err := dbStore.ListBackendsForAffinityGroup(ctx, group.ID)
		if err != nil {
			return fmt.Errorf("fetching backends for group %s: %v", group.ID, err)
		}

		groupModels, err := dbStore.ListModelsForAffinityGroup(ctx, group.ID)
		if err != nil {
			return fmt.Errorf("fetching models for group %s: %v", group.ID, err)
		}

		for _, backend := range groupBackends {
			if matchesProvider(backend, providers) {
				activeBackendIDs[backend.ID] = struct{}{}
			}
			if _, exists := allBackendObjects[backend.ID]; !exists {
				allBackendObjects[backend.ID] = backend
			}
			if _, exists := backendToAggregatedModels[backend.ID]; !exists {
				backendToAggregatedModels[backend.ID] = make(map[string]*runtimetypes.Model)
			}
			for _, model := range groupModels {
				backendToAggregatedModels[backend.ID][model.Model] = model
			}
		}
	}

	jobs := make([]backendJob, 0, len(allBackendObjects))
	for backendID, backendObj := range allBackendObjects {
		modelsForThisBackend := make([]*runtimetypes.Model, 0, len(backendToAggregatedModels[backendID]))
		for _, model := range backendToAggregatedModels[backendID] {
			modelsForThisBackend = append(modelsForThisBackend, model)
		}
		if matchesProvider(backendObj, providers) {
			jobs = append(jobs, backendJob{backendObj, modelsForThisBackend})
		}
	}
	if err := s.runBackendJobs(ctx, jobs); err != nil {
		return err
	}
	return s.cleanupProviderBackends(ctx, activeBackendIDs, providers)
}

func (s *State) syncBackends(ctx context.Context, providers []string) error {
	tx := s.dbInstance.WithoutTransaction()
	storeInstance := runtimetypes.New(tx)

	backends, err := storeInstance.ListAllBackends(ctx)
	if err != nil {
		return fmt.Errorf("fetching backends: %w", err)
	}

	allModels, err := storeInstance.ListAllModels(ctx)
	if err != nil {
		return fmt.Errorf("fetching paginated models: %v", err)
	}

	currentIDs := make(map[string]struct{})
	jobs := make([]backendJob, 0, len(backends))
	for _, backend := range backends {
		if matchesProvider(backend, providers) {
			currentIDs[backend.ID] = struct{}{}
			jobs = append(jobs, backendJob{backend, allModels})
		}
	}
	if err := s.runBackendJobs(ctx, jobs); err != nil {
		return err
	}
	return s.cleanupProviderBackends(ctx, currentIDs, providers)
}

func (s *State) processBackend(ctx context.Context, backend *runtimetypes.Backend, declaredModels []*runtimetypes.Model) {
	defer func() {
		if err := s.ReconcileDeclarations(ctx, backend.ID); err != nil {
			storeBackendError(s, backend, "", err, nil)
		}
	}()
	switch modelrepo.CanonicalBackendType(backend.Type) {
	case "modeld", "local":
		s.processModeldBackend(ctx, backend, declaredModels)
	case "ollama":
		s.processOllamaBackend(ctx, backend, declaredModels)
	case "vllm":
		s.processVLLMBackend(ctx, backend, declaredModels)
	case "gemini":
		s.processGeminiBackend(ctx, backend, declaredModels)
	case "openai", "anthropic":
		s.processOpenAIBackend(ctx, backend, declaredModels)
	case "openai-codex":
		s.processCodexBackend(ctx, backend, declaredModels)
	case "vertex-google":
		s.processVertexBackend(ctx, backend, declaredModels)
	case "bedrock":
		s.processBedrockBackend(ctx, backend, declaredModels)
	case modelrepo.ScriptedTestBackendType:
		s.processScriptedTestBackend(ctx, backend)
	default:
		if modelrepo.HasCatalogProvider(backend.Type) {
			s.processOpenAIBackend(ctx, backend, declaredModels)
			return
		}
		brokenService := &BackendRuntimeState{
			ID:      backend.ID,
			Name:    backend.Name,
			Models:  []string{},
			Backend: *backend,
			Error:   "Unsupported backend type: " + backend.Type,
		}
		s.state.Store(backend.ID, brokenService)
	}
}

func (s *State) processModeldBackend(ctx context.Context, backend *runtimetypes.Backend, declaredModels []*runtimetypes.Model) {
	models := declaredModelNames(declaredModels)

	catalog, err := s.newCatalogProvider(backend, "")
	if err != nil {
		storeBackendError(s, backend, "", err, models)
		return
	}

	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		storeBackendError(s, backend, "", err, models)
		return
	}

	stateservice := &BackendRuntimeState{
		ID:      backend.ID,
		Name:    backend.Name,
		Backend: *backend,
		Models:  make([]string, 0, len(observedModels)),
	}
	stateservice.SetAPIKey("")

	pulledModels := make([]ModelPullStatus, 0, len(observedModels))
	for _, observed := range observedModels {
		lmr := pullStatusFromObservedModel(observed)
		pulledModels = append(pulledModels, lmr)
	}

	stateservice.PulledModels = pulledModels
	stateservice.Models = observedModelNames(observedModels)
	s.state.Store(backend.ID, stateservice)
}

func (s *State) processOllamaBackend(ctx context.Context, backend *runtimetypes.Backend, declaredOllamaModels []*runtimetypes.Model) {
	declaredModelMap := declaredModelsByName(declaredOllamaModels)
	models := declaredModelNames(declaredOllamaModels)

	apiKey, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		storeBackendError(s, backend, "", err, models)
		return
	}

	catalog, err := s.newCatalogProvider(backend, apiKey)
	if err != nil {
		storeBackendError(s, backend, apiKey, err, models)
		return
	}

	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		storeBackendError(s, backend, apiKey, err, models)
		return
	}

	stateservice := &BackendRuntimeState{
		ID:      backend.ID,
		Name:    backend.Name,
		Backend: *backend,
		Models:  make([]string, 0, len(observedModels)),
	}
	stateservice.SetAPIKey(apiKey)

	pulledModels := make([]ModelPullStatus, 0, len(observedModels))
	for _, observed := range observedModels {
		lmr := s.applyDeclaredModel(ctx, backend, declaredModelMap[observed.Name], observed)
		pulledModels = append(pulledModels, lmr)
	}

	stateservice.PulledModels = pulledModels
	if s.autoDiscoverModels {
		stateservice.Models = observedModelNames(observedModels)
	} else {
		stateservice.Models = models
	}
	s.state.Store(backend.ID, stateservice)
}

func (s *State) processVLLMBackend(ctx context.Context, backend *runtimetypes.Backend, models []*runtimetypes.Model) {
	declaredModelMap := declaredModelsByName(models)

	apiKey, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		storeBackendError(s, backend, "", err, nil)
		return
	}

	catalog, err := s.newCatalogProvider(backend, apiKey)
	if err != nil {
		storeBackendError(s, backend, apiKey, err, nil)
		return
	}

	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		storeBackendError(s, backend, apiKey, err, nil)
		return
	}
	if len(observedModels) == 0 {
		storeBackendError(s, backend, apiKey, fmt.Errorf("No models found in response"), nil)
		return
	}

	res := &BackendRuntimeState{
		ID:      backend.ID,
		Name:    backend.Name,
		Models:  observedModelNames(observedModels),
		Backend: *backend,
	}
	res.SetAPIKey(apiKey)

	pulledModels := make([]ModelPullStatus, 0, len(observedModels))
	for _, observed := range observedModels {
		declared, exists := declaredModelMap[observed.Name]
		if !exists && !s.autoDiscoverModels {
			continue
		}
		lmr := s.applyDeclaredModel(ctx, backend, declared, observed)
		pulledModels = append(pulledModels, lmr)
	}

	if len(declaredModelMap) > 0 && len(pulledModels) == 0 && !s.autoDiscoverModels {
		res.Error = declaredModelsUnavailableError("vLLM", declaredModelMap, res.Models).Error()
	}
	res.PulledModels = pulledModels
	s.state.Store(backend.ID, res)
}

func (s *State) processGeminiBackend(ctx context.Context, backend *runtimetypes.Backend, declared []*runtimetypes.Model) {
	declaredModels := declaredModelsByName(declared)
	stateInstance := &BackendRuntimeState{
		ID:           backend.ID,
		Name:         backend.Name,
		Backend:      *backend,
		PulledModels: []ModelPullStatus{},
	}
	stateInstance.SetAPIKey("")
	apiKey, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil {
		stateInstance.Error = apiKeyFailure(err)
		s.state.Store(backend.ID, stateInstance)
		return
	}
	stateInstance.SetAPIKey(apiKey)

	if cachedModels, ok := s.loadObservedModelCache(ctx, backend.ID, apiKey); ok {
		stateInstance.Models = observedModelNames(cachedModels)
		stateInstance.PulledModels = make([]ModelPullStatus, 0, len(cachedModels))
		for _, model := range cachedModels {
			lmr := s.applyDeclaredModel(ctx, backend, declaredModels[model.Name], model)
			stateInstance.PulledModels = append(stateInstance.PulledModels, lmr)
		}
		s.state.Store(backend.ID, stateInstance)
		return
	}

	catalog, err := s.newCatalogProvider(backend, apiKey)
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}
	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}

	stateInstance.Models = observedModelNames(observedModels)
	stateInstance.PulledModels = make([]ModelPullStatus, 0, len(observedModels))
	for _, model := range observedModels {
		lmr := s.applyDeclaredModel(ctx, backend, declaredModels[model.Name], model)
		stateInstance.PulledModels = append(stateInstance.PulledModels, lmr)
	}
	s.state.Store(backend.ID, stateInstance)

	s.storeObservedModelCache(ctx, backend.ID, apiKey, observedModels, catalog)
}

func (s *State) processVertexBackend(ctx context.Context, backend *runtimetypes.Backend, models []*runtimetypes.Model) {
	s.processOptionalCredCloudBackend(ctx, backend, models)
}

func (s *State) processBedrockBackend(ctx context.Context, backend *runtimetypes.Backend, models []*runtimetypes.Model) {
	s.processOptionalCredCloudBackend(ctx, backend, models)
}

func (s *State) processOptionalCredCloudBackend(ctx context.Context, backend *runtimetypes.Backend, declared []*runtimetypes.Model) {
	declaredModels := declaredModelsByName(declared)
	stateInstance := &BackendRuntimeState{
		ID:           backend.ID,
		Name:         backend.Name,
		Backend:      *backend,
		PulledModels: []ModelPullStatus{},
	}

	credJSON, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}
	if err == nil {
		stateInstance.SetAPIKey(credJSON)
	}

	if cachedModels, ok := s.loadObservedModelCache(ctx, backend.ID, credJSON); ok {
		stateInstance.Models = observedModelNames(cachedModels)
		stateInstance.PulledModels = make([]ModelPullStatus, 0, len(cachedModels))
		for _, model := range cachedModels {
			lmr := s.applyDeclaredModel(ctx, backend, declaredModels[model.Name], model)
			stateInstance.PulledModels = append(stateInstance.PulledModels, lmr)
		}
		s.state.Store(backend.ID, stateInstance)
		return
	}

	catalog, err := s.newCatalogProvider(backend, credJSON)
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}
	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}

	stateInstance.Models = observedModelNames(observedModels)
	stateInstance.PulledModels = make([]ModelPullStatus, 0, len(observedModels))
	for _, model := range observedModels {
		lmr := s.applyDeclaredModel(ctx, backend, declaredModels[model.Name], model)
		stateInstance.PulledModels = append(stateInstance.PulledModels, lmr)
	}
	s.state.Store(backend.ID, stateInstance)
	s.storeObservedModelCache(ctx, backend.ID, credJSON, observedModels, catalog)
}

func apiKeyFailure(err error) string {
	var unset *EnvironmentUnsetError
	switch {
	case errors.Is(err, libdb.ErrNotFound):
		return "API key not configured"
	case errors.As(err, &unset):
		return err.Error()
	}
	return fmt.Sprintf("Failed to retrieve API key configuration: %v", err)
}

func (s *State) processScriptedTestBackend(ctx context.Context, backend *runtimetypes.Backend) {
	stateInstance := &BackendRuntimeState{
		ID:           backend.ID,
		Name:         backend.Name,
		Backend:      *backend,
		PulledModels: []ModelPullStatus{},
	}
	stateInstance.SetAPIKey("")

	catalog, err := s.newCatalogProvider(backend, "")
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}
	observedModels, err := catalog.ListModels(ctx)
	if err != nil {
		stateInstance.Error = err.Error()
		s.state.Store(backend.ID, stateInstance)
		return
	}

	stateInstance.Models = observedModelNames(observedModels)
	for _, model := range observedModels {
		stateInstance.PulledModels = append(stateInstance.PulledModels, s.applyDeclarations(ctx, backend, pullStatusFromObservedModel(model)))
	}
	s.state.Store(backend.ID, stateInstance)
	s.storeObservedModelCache(ctx, backend.ID, "", observedModels, catalog)
}

func (s *State) processOpenAIBackend(ctx context.Context, backend *runtimetypes.Backend, models []*runtimetypes.Model) {
	stateInstance := &BackendRuntimeState{
		ID:           backend.ID,
		Name:         backend.Name,
		PulledModels: []ModelPullStatus{},
		Backend:      *backend,
	}

	apiKey, err := s.loadProviderAPIKey(ctx, backend)
	if err != nil {
		stateInstance.Error = apiKeyFailure(err)
		s.state.Store(backend.ID, stateInstance)
		return
	}
	stateInstance.SetAPIKey(apiKey)

	declaredModels := make(map[string]*runtimetypes.Model)
	for _, model := range models {
		name, _ := strings.CutSuffix(model.Model, ":latest")
		declaredModels[name] = model
	}

	observedModels, ok := s.loadObservedModelCache(ctx, backend.ID, apiKey)
	if !ok {
		catalog, err := s.newCatalogProvider(backend, apiKey)
		if err != nil {
			stateInstance.Error = err.Error()
			s.state.Store(backend.ID, stateInstance)
			return
		}
		observedModels, err = catalog.ListModels(ctx)
		if err != nil {
			stateInstance.Error = err.Error()
			s.state.Store(backend.ID, stateInstance)
			return
		}
		s.storeObservedModelCache(ctx, backend.ID, apiKey, observedModels, catalog)
	}

	stateInstance.Models = observedModelNames(observedModels)
	pulledModels := make([]ModelPullStatus, 0, len(observedModels))
	for _, observed := range observedModels {
		declared, exists := declaredModels[observed.Name]
		if !exists && !s.autoDiscoverModels {
			continue
		}
		lmr := s.applyDeclaredModel(ctx, backend, declared, observed)
		pulledModels = append(pulledModels, lmr)
	}
	stateInstance.PulledModels = pulledModels
	if len(declaredModels) > 0 && len(pulledModels) == 0 && !s.autoDiscoverModels {
		stateInstance.Error = declaredModelsUnavailableError("OpenAI", declaredModels, stateInstance.Models).Error()
	}

	s.state.Store(backend.ID, stateInstance)
}
