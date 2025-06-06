package testingsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libbus"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/contenox/contenox/libs/libroutine"
	"github.com/contenox/contenox/libs/libtestenv"
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

func (builder *Builder) WithServiceManager(expiry string) *Builder {
	if builder.Err != nil {
		return builder
	}

	// Track this operation
	reportErr, _, end := builder.tracker.Start(builder.ctx, "setup", "service_manager")
	defer end()

	err := serverops.NewServiceManager(&serverops.Config{
		JWTExpiry: expiry,
	})
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
		Type:    "Ollama",
	})
	if err != nil {
		builder.Err = fmt.Errorf("failed to create backend: %v", err)
		reportErr(err)
		return builder
	}

	reportChange(backendID, map[string]interface{}{
		"name":   "test-backend",
		"type":   "Ollama",
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

	// Wait for the condition
	err := WaitForCondition(builder.ctx, func() bool {
		currentState := builder.state.Get(builder.ctx)
		data, err := json.Marshal(currentState)
		if err != nil {
			return false
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

func (builder *Builder) Build(ctx context.Context) (context.Context, *runtimestate.State, libdb.DBManager, func(), error) {
	if builder.Err != nil {
		return ctx, nil, nil, nil, builder.Err
	}
	if builder.state == nil {
		builder.Err = fmt.Errorf("state is nil")
		return ctx, nil, nil, nil, builder.Err
	}
	if builder.dbManager == nil {
		builder.Err = fmt.Errorf("dbManager is nil")
		return ctx, nil, nil, nil, builder.Err
	}
	return ctx, builder.state, builder.dbManager, func() {
		for _, v := range builder.cleanups {
			v()
		}
	}, builder.Err
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

// Modified SetupTestEnvironment to optionally accept an activity tracker
func SetupTestEnvironment(config *serverops.Config, tracker serverops.ActivityTracker) (context.Context, *runtimestate.State, libdb.DBManager, func(), error) {
	ctx := context.TODO()
	if config == nil {
		config = DefaultConfig()
	}

	// Use noop tracker if none provided
	if tracker == nil {
		tracker = serverops.NoopTracker{}
	}

	// Initialize the builder with the tracker
	builder := New(ctx, tracker)
	wait := true
	// Track the overall test environment setup
	reportErr, reportChange, end := tracker.Start(ctx, "setup", "test_environment")
	defer end()

	// Initialize serverops service manager
	err := serverops.NewServiceManager(config)
	if err != nil {
		reportErr(err)
		return nil, nil, nil, func() {}, err
	}

	// Setup the test environment using the builder
	builder = builder.WithTriggerChan().
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		WithModel("smollm2:135m").
		RunState().
		RunDownloadManager()

	if config.JWTExpiry != "" {
		builder = builder.WithServiceManager(config.JWTExpiry)
	}

	if wait {
		builder = builder.WaitForModel("smollm2:135m")
	}

	// If we have any errors during setup, return them
	if builder.Err != nil {
		reportErr(builder.Err)
		return nil, nil, nil, func() {}, builder.Err
	}

	reportChange("test_environment", map[string]interface{}{
		"status": "completed",
		"components": []string{
			"trigger_chan",
			"db_conn",
			"db_manager",
			"pubsub",
			"ollama",
			"state",
			"backend",
			"model",
			"state_runner",
		},
	})

	// Build and return the components
	return builder.Build(ctx)
}
