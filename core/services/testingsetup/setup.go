package testingsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/ollamatokenizer"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime-mvp/core/services/dispatchservice"
	"github.com/contenox/runtime-mvp/core/services/fileservice"
	"github.com/contenox/runtime-mvp/core/services/indexservice"
	"github.com/contenox/runtime-mvp/core/services/userservice"
	"github.com/contenox/runtime-mvp/libs/libbus"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libmodelprovider"
	"github.com/contenox/runtime-mvp/libs/libroutine"
	"github.com/contenox/runtime-mvp/libs/libtestenv"
	"github.com/google/uuid"
)

type Builder struct {
	Err          error
	ctx          context.Context
	state        *runtimestate.State
	dbManager    libdb.DBManager
	dbConn       string
	schema       string
	ps           libbus.Messenger
	ollamaURI    string
	triggerChan  chan struct{}
	cleanups     []func()
	stateErrs    []error
	downloadErrs []error
	tracker      serverops.ActivityTracker
	backends     []string
}

func New(ctx context.Context, tracker serverops.ActivityTracker) *Builder {
	if tracker == nil {
		tracker = serverops.NoopTracker{}
	}

	return &Builder{
		schema:  store.Schema,
		ctx:     ctx,
		tracker: tracker,
	}
}

func (builder *Builder) WithServiceManager(config *serverops.Config) *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "service_manager")
	defer end()

	err := serverops.NewServiceManager(config)
	if err != nil {
		builder.Err = fmt.Errorf("failed to create new service manager: %v", err)
		reportErr(err)
	}
	return builder
}

func (builder *Builder) AddCleanup(fn func()) {
	if fn == nil {
		return
	}
	builder.cleanups = append(builder.cleanups, fn)
}

func (builder *Builder) WithTriggerChan() *Builder {
	if builder.Err != nil {
		return builder
	}
	builder.triggerChan = make(chan struct{})
	builder.AddCleanup(func() {
		close(builder.triggerChan)
	})
	return builder
}

func (builder *Builder) WithDBManager() *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "db_manager")
	defer end()

	if builder.dbConn == "" {
		builder.Err = errors.New("dbConn is empty")
		reportErr(builder.Err)
		return builder
	}

	dbInstance, err := libdb.NewPostgresDBManager(builder.ctx, builder.dbConn, builder.schema)
	if err != nil {
		builder.Err = fmt.Errorf("failed to create new db manager: %v", err)
		reportErr(err)
		return builder
	}

	builder.dbManager = dbInstance
	return builder
}

func (builder *Builder) WithDBConn(dbName string) *Builder {
	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "db_connection")
	defer end()

	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(builder.ctx, uuid.NewString(), dbName, "test")
	builder.AddCleanup(dbCleanup)
	if err != nil {
		builder.Err = fmt.Errorf("failed to setup local database: %v", err)
		reportErr(err)
		return builder
	}

	builder.dbConn = dbConn
	return builder
}

func (builder *Builder) WithSchema(schema string) *Builder {
	builder.schema = schema
	return builder
}

func (builder *Builder) WithPubSub() *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "pubsub")
	defer end()

	ps, cleanup2, err := libbus.NewTestPubSub()
	builder.AddCleanup(cleanup2)
	if err != nil {
		builder.Err = fmt.Errorf("failed to init pubsub: %v", err)
		reportErr(err)
		return builder
	}

	builder.ps = ps
	return builder
}

func (builder *Builder) WithState() *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "state")
	defer end()

	backendState, err := runtimestate.New(builder.ctx, builder.dbManager, builder.ps)
	if err != nil {
		builder.Err = fmt.Errorf("failed to create new backend state: %v", err)
		reportErr(err)
		return builder
	}

	builder.state = backendState
	return builder
}

func (builder *Builder) WithOllama() *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "ollama")
	defer end()

	ollamaURI, _, ollamaCleanup, err := libtestenv.SetupOllamaLocalInstance(builder.ctx)
	if err != nil {
		builder.Err = fmt.Errorf("failed to start local Ollama instance: %v", err)
		reportErr(err)
		return builder
	}

	builder.ollamaURI = ollamaURI
	builder.AddCleanup(ollamaCleanup)
	return builder
}

func (builder *Builder) WithDefaultUser() *Builder {
	return builder.WithUser("John Doe", "string@strings.com", serverops.DefaultAdminUser)
}

func (builder *Builder) WithBackend() *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, reportChange, end := builder.tracker.Start(builder.ctx, "create", "backend")
	defer end()

	dbStore := store.New(builder.dbManager.WithoutTransaction())
	backendID := uuid.NewString()
	err := dbStore.CreateBackend(builder.ctx, &store.Backend{
		ID:      backendID,
		Name:    "test-backend",
		BaseURL: builder.ollamaURI,
		Type:    "ollama",
	})
	builder.backends = append(builder.backends, backendID)
	if err != nil {
		builder.Err = fmt.Errorf("failed to create backend: %v", err)
		reportErr(err)
		return builder
	}

	reportChange(backendID, map[string]interface{}{
		"name":   "test-backend",
		"type":   "ollama",
		"status": "created",
	})

	return builder
}

func (builder *Builder) WithModel(model string) *Builder {
	if builder.Err != nil {
		return builder
	}

	if model == "" {
		builder.Err = fmt.Errorf("model cannot be empty")
		return builder
	}

	// Track this operation
	reportErr, reportChange, end := builder.tracker.Start(builder.ctx, "create", "model")
	defer end()

	storeInstance := store.New(builder.dbManager.WithoutTransaction())
	modelID := uuid.NewString()

	err := storeInstance.AppendModel(builder.ctx, &store.Model{
		Model: model,
		ID:    modelID,
	})
	if err != nil {
		builder.Err = fmt.Errorf("failed to append model: %v", err)
		reportErr(err)
		return builder
	}

	reportChange(modelID, map[string]interface{}{
		"name":   model,
		"status": "appended",
	})

	return builder
}

func (builder *Builder) WithUser(email string, friendlyName string, subjectID string) *Builder {
	if builder.Err != nil {
		return builder
	}

	if email == "" || friendlyName == "" {
		builder.Err = fmt.Errorf("email and friendlyName cannot be empty")
		return builder
	}

	// Track this operation
	reportErr, reportChange, end := builder.tracker.Start(builder.ctx, "create", "user")
	defer end()

	storeInstance := store.New(builder.dbManager.WithoutTransaction())
	userID := uuid.NewString()

	err := storeInstance.CreateUser(builder.ctx, &store.User{
		ID:           userID,
		FriendlyName: friendlyName,
		Email:        email,
		Subject:      subjectID,
	})
	if err != nil {
		builder.Err = fmt.Errorf("failed to create user: %v", err)
		reportErr(err)
		return builder
	}

	reportChange(userID, map[string]interface{}{
		"email":  email,
		"name":   friendlyName,
		"status": "created",
	})

	return builder
}

func (builder *Builder) WaitForModel(model string) *Builder {
	if builder.Err != nil {
		return builder
	}
	if builder.state == nil {
		builder.Err = fmt.Errorf("state is nil")
		return builder
	}

	// Track this operation
	reportErr, reportChange, end := builder.tracker.Start(
		builder.ctx, "wait", "model_pull",
		"model", model,
	)
	defer end()

	// Trigger a sync
	builder.triggerChan <- struct{}{}
	i := 0
	// Wait for the condition
	err := WaitForCondition(builder.ctx, func() bool {
		currentState := builder.state.Get(builder.ctx)
		data, err := json.Marshal(currentState)
		if err != nil {
			return false
		}
		i++
		if i%10 == 0 {
			reportChange(model, map[string]interface{}{
				"status": "waiting",
				"waited": i,
			})
		}
		return bytes.Contains(data, []byte(fmt.Sprintf(`"pulledModels":[{"name":"%s"`, model)))
	}, 2*time.Minute, 100*time.Millisecond)
	if err != nil {
		builder.Err = fmt.Errorf("timeout waiting for condition: %v", err)
		reportErr(builder.Err)
		return builder
	}

	// Report that the model was successfully pulled
	reportChange(model, map[string]interface{}{
		"status": "pulled",
		"waited": true,
	})

	return builder
}

func (builder *Builder) RunState() *Builder {
	if builder.Err != nil {
		return builder
	}

	if builder.state == nil {
		builder.Err = fmt.Errorf("state is nil")
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "start", "state_service")
	defer end()

	// Use the circuit breaker loop to run the state service cycles.
	breaker := libroutine.NewRoutine(3, 1*time.Second)
	go breaker.Loop(builder.ctx, time.Second, builder.triggerChan, builder.state.RunBackendCycle, func(err error) {
		if err != nil {
			builder.stateErrs = append(builder.stateErrs, err)
			reportErr(err)
		}
	})

	return builder
}

func (builder *Builder) RunDownloadManager() *Builder {
	if builder.Err != nil {
		return builder
	}
	if builder.state == nil {
		builder.Err = fmt.Errorf("state is nil")
		return builder
	}
	breaker2 := libroutine.NewRoutine(3, 1*time.Second)
	go breaker2.Loop(builder.ctx, time.Second, builder.triggerChan, builder.state.RunDownloadCycle, func(err error) {
		if err != nil {
			builder.downloadErrs = append(builder.downloadErrs, err)
		}
	})
	return builder
}

func WaitForCondition(ctx context.Context, condition func() bool, timeout, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if condition() {
				return nil
			}
		}
	}
}

func DefaultConfig() *serverops.Config {
	return &serverops.Config{
		JWTExpiry: "1h",
	}
}

type Environment struct {
	Ctx         context.Context
	state       *runtimestate.State
	dbManager   libdb.DBManager
	cleanups    []func()
	Err         error
	backends    []string
	triggerChan chan struct{}
	tokenizer   ollamatokenizer.Tokenizer
	tracker     serverops.ActivityTracker
}

func (builder *Builder) Build() *Environment {
	// if builder.Err != nil {
	// 	return &Environment{
	// 		Ctx:         builder.ctx,
	// 		state:       nil,
	// 		dbManager:   nil,
	// 		cleanups:    nil,
	// 		Err:         builder.Err,
	// 		triggerChan: builder.triggerChan,
	// 		backends:    builder.backends,
	// 		tracker:     builder.tracker,
	// 	}
	// }
	// if builder.state == nil {
	// 	builder.Err = fmt.Errorf("state is nil")
	// 	return &Environment{
	// 		Ctx:         builder.ctx,
	// 		state:       nil,
	// 		dbManager:   nil,
	// 		cleanups:    nil,
	// 		Err:         builder.Err,
	// 		triggerChan: builder.triggerChan,
	// 		backends:    builder.backends,
	// 		tracker:     builder.tracker,
	// 	}
	// }
	// if builder.dbManager == nil {
	// 	builder.Err = fmt.Errorf("dbManager is nil")
	// 	return &Environment{
	// 		Ctx:         builder.ctx,
	// 		state:       nil,
	// 		dbManager:   nil,
	// 		cleanups:    nil,
	// 		Err:         builder.Err,
	// 		triggerChan: builder.triggerChan,
	// 		backends:    builder.backends,
	// 		tracker:     builder.tracker,
	// 	}
	// }
	return &Environment{
		Ctx:         builder.ctx,
		state:       builder.state,
		dbManager:   builder.dbManager,
		cleanups:    builder.cleanups,
		Err:         builder.Err,
		triggerChan: builder.triggerChan,
		backends:    builder.backends,
		tracker:     builder.tracker,
	}
}

func (env *Environment) Cleanup() {
	for _, cleanup := range env.cleanups {
		cleanup()
	}
}

func (env *Environment) Unzip() (context.Context, *runtimestate.State, libdb.DBManager, func(), error) {
	if env.Err != nil {
		return nil, nil, nil, nil, env.Err
	}
	if env.state == nil {
		env.Err = fmt.Errorf("state is nil")
		return nil, nil, nil, nil, env.Err
	}
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		return nil, nil, nil, nil, env.Err
	}
	return env.Ctx, env.state, env.dbManager, env.Cleanup, nil
}

func (env *Environment) Store() store.Store {
	if env.Err != nil {
		return nil
	}
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		return nil
	}
	return store.New(env.dbManager.WithoutTransaction())
}

func (env *Environment) State() map[string]runtimestate.LLMState {
	if env.Err != nil {
		return nil
	}
	if env.state == nil {
		env.Err = fmt.Errorf("state is nil")
		return nil
	}
	runtimeState := env.state.Get(env.Ctx)
	return runtimeState
}

func (env *Environment) AssignBackends(pool string) *Environment {
	if env.Err != nil {
		return env
	}
	// Track this operation
	reportErr, reportChange, end := env.tracker.Start(env.Ctx, "assign", "backends", "pool", pool)
	defer end()

	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		reportErr(env.Err)
		return env
	}
	if env.backends == nil {
		env.Err = fmt.Errorf("backends is nil")
		reportErr(env.Err)
		return env
	}

	store := store.New(env.dbManager.WithoutTransaction())
	for i, v := range env.backends {
		err := store.AssignBackendToPool(env.Ctx, pool, v)
		if err != nil {
			env.Err = err
			reportErr(err)
			return env
		}
		// Report each assignment
		reportChange(v, map[string]interface{}{
			"pool":   pool,
			"index":  i,
			"status": "assigned",
		})
	}
	return env
}

func (env *Environment) NewEmbedder(config *serverops.Config) (llmrepo.ModelRepo, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	// Track this operation
	reportErr, _, end := env.tracker.Start(env.Ctx, "create", "embedder")
	defer end()

	if env.state == nil {
		err := fmt.Errorf("state is nil")
		env.Err = err
		reportErr(err)
		return nil, err
	}
	if env.dbManager == nil {
		err := fmt.Errorf("dbManager is nil")
		env.Err = err
		reportErr(err)
		return nil, err
	}

	repo, err := llmrepo.NewEmbedder(env.Ctx, config, env.dbManager, env.state)
	if err != nil {
		env.Err = err
		reportErr(err)
	}
	return repo, err
}

func (env *Environment) GetEmbedConnection(provider libmodelprovider.Provider) (libmodelprovider.LLMEmbedClient, error) {
	if env.Err != nil {
		return nil, env.Err
	}

	if env.state == nil {
		env.Err = fmt.Errorf("state is nil")
		return nil, env.Err
	}
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		return nil, env.Err
	}

	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	backends := provider.GetBackendIDs()

	if len(backends) == 0 {
		return nil, fmt.Errorf("no backends found")
	}
	if len(backends) > 1 {
		return nil, fmt.Errorf("multiple backends found")
	}
	baseURL := backends[0]

	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is empty")
	}

	return provider.GetEmbedConnection(env.Ctx, baseURL)
}

func (env *Environment) GetPromptConnection(provider libmodelprovider.Provider) (libmodelprovider.LLMPromptExecClient, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if env.state == nil {
		env.Err = fmt.Errorf("state is nil")
		return nil, env.Err
	}
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		return nil, env.Err
	}

	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	backends := provider.GetBackendIDs()

	if len(backends) == 0 {
		return nil, fmt.Errorf("no backends found")
	}
	if len(backends) > 1 {
		return nil, fmt.Errorf("multiple backends found")
	}
	baseURL := backends[0]

	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is empty")
	}

	return provider.GetPromptConnection(env.Ctx, baseURL)
}

func (env *Environment) GetDBInstance() libdb.DBManager {
	return env.dbManager
}

func (env *Environment) NewExecRepo(config *serverops.Config) (llmrepo.ModelRepo, error) {
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		return nil, env.Err
	}

	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.tokenizer == nil {
		env.Err = fmt.Errorf("tokenizer is nil")
		return nil, env.Err
	}

	return llmrepo.NewExecRepo(env.Ctx, config, env.dbManager, env.state, env.tokenizer)
}

func (env *Environment) WaitForModel(model string) *Environment {
	if env.Err != nil {
		return env
	}

	// Track this operation - include model name in attributes
	reportErr, reportChange, end := env.tracker.Start(
		env.Ctx,
		"wait",
		"model_pull",
		"model", model,
	)
	defer end()

	// Error checks with reporting
	if env.dbManager == nil {
		env.Err = fmt.Errorf("dbManager is nil")
		reportErr(env.Err)
		return env
	}
	if env.state == nil {
		env.Err = fmt.Errorf("state is nil")
		reportErr(env.Err)
		return env
	}
	if env.backends == nil {
		env.Err = fmt.Errorf("backends is nil")
		reportErr(env.Err)
		return env
	}

	// Trigger a sync to start model pulling
	select {
	case env.triggerChan <- struct{}{}:
		// Successfully triggered
	default:
		// Channel full, but not critical
	}

	// Wait for the condition
	err := WaitForCondition(env.Ctx, func() bool {
		currentState := env.state.Get(env.Ctx)
		data, err := json.Marshal(currentState)
		if err != nil {
			return false
		}
		return bytes.Contains(data, []byte(fmt.Sprintf(`"pulledModels":[{"name":"%s"`, model)))
	}, 3*time.Minute, 2*time.Second)
	// Handle wait result
	if err != nil {
		env.Err = fmt.Errorf("timeout waiting for model %s: %w", model, err)
		reportErr(env.Err)
		return env
	}

	// Report successful pull
	reportChange(model, map[string]interface{}{
		"status": "pulled",
		"waited": true,
	})

	return env
}

func (env *Environment) NewFileservice(config *serverops.Config) (fileservice.Service, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.dbManager == nil {
		return nil, fmt.Errorf("dbManager is nil")
	}

	if env.state == nil {
		return nil, fmt.Errorf("state is nil")
	}

	if env.backends == nil {
		return nil, fmt.Errorf("backends is nil")
	}

	return fileservice.New(env.dbManager, config), nil
}

func (env *Environment) NewFileVectorizationJobCreator(config *serverops.Config) (serverops.ActivityTracker, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.dbManager == nil {
		return nil, fmt.Errorf("dbManager is nil")
	}

	if env.state == nil {
		return nil, fmt.Errorf("state is nil")
	}

	if env.backends == nil {
		return nil, fmt.Errorf("backends is nil")
	}

	return fileservice.NewFileVectorizationJobCreator(env.dbManager), nil
}

func (env *Environment) NewIndexService(config *serverops.Config, vectorstore vectors.Store, embedder llmrepo.ModelRepo, promptExec llmrepo.ModelRepo) (indexservice.Service, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.dbManager == nil {
		return nil, fmt.Errorf("dbManager is nil")
	}

	if env.state == nil {
		return nil, fmt.Errorf("state is nil")
	}

	if env.backends == nil {
		return nil, fmt.Errorf("backends is nil")
	}

	return indexservice.New(env.Ctx, embedder, promptExec, vectorstore, env.dbManager), nil
}

func (env *Environment) NewUserservice(config *serverops.Config) (userservice.Service, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.dbManager == nil {
		return nil, fmt.Errorf("dbManager is nil")
	}

	return userservice.New(env.dbManager, config), nil
}

func (env *Environment) NewDispatchService(config *serverops.Config) (dispatchservice.Service, error) {
	if env.Err != nil {
		return nil, env.Err
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if env.dbManager == nil {
		return nil, fmt.Errorf("dbManager is nil")
	}

	if env.state == nil {
		return nil, fmt.Errorf("state is nil")
	}

	if env.backends == nil {
		return nil, fmt.Errorf("backends is nil")
	}

	return dispatchservice.New(env.dbManager, config), nil
}
