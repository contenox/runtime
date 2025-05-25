package testingsetup

import (
	"bytes"
	"context"
	"encoding/json"
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

func SetupTestEnvironment(config *serverops.Config) (context.Context, *runtimestate.State, libdb.DBManager, func(), error) {
	ctx := context.TODO()
	var cleanups []func()
	err := serverops.NewServiceManager(config)
	if err != nil {
		return nil, nil, nil, func() {}, err
	}
	// We'll collect cleanup functions as we go.
	addCleanup := func(fn func()) {
		cleanups = append(cleanups, fn)
	}

	// Start local Ollama instance.
	ollamaURI, _, ollamaCleanup, err := libtestenv.SetupOllamaLocalInstance(ctx)
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("failed to start local Ollama instance: %v", err)
	}
	addCleanup(ollamaCleanup)

	// Initialize test database.
	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(ctx, uuid.NewString(), "test", "test")
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, func() {}, fmt.Errorf("failed to setup local database: %v", err)
	}
	addCleanup(dbCleanup)

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, func() {}, fmt.Errorf("failed to create new Postgres DB Manager: %v", err)
	}
	ps, cleanup2, err := libbus.NewTestPubSub()
	addCleanup(cleanup2)
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("failed to init pubsub: %v", err)
	}
	// Initialize backend service state.
	backendState, err := runtimestate.New(ctx, dbInstance, ps)
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, func() {}, fmt.Errorf("failed to create new backend state: %v", err)
	}

	triggerChan := make(chan struct{})
	// Use the circuit breaker loop to run the state service cycles.
	breaker := libroutine.NewRoutine(3, 1*time.Second)
	go breaker.Loop(ctx, time.Second, triggerChan, backendState.RunBackendCycle, func(err error) {})
	breaker2 := libroutine.NewRoutine(3, 1*time.Second)
	go breaker2.Loop(ctx, time.Second, triggerChan, backendState.RunDownloadCycle, func(err error) {})
	// Register cleanup for the trigger channel.
	addCleanup(func() { close(triggerChan) })

	// Create backend and append model.
	dbStore := store.New(dbInstance.WithoutTransaction())
	backendID := uuid.NewString()
	err = dbStore.CreateBackend(ctx, &store.Backend{
		ID:      backendID,
		Name:    "test-backend",
		BaseURL: ollamaURI,
		Type:    "Ollama",
	})
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, func() {}, fmt.Errorf("failed to create backend: %v", err)
	}
	// Append model to the global model store.
	err = dbStore.AppendModel(ctx, &store.Model{
		Model: "smollm2:135m",
		ID:    uuid.NewString(),
	})
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, func() {}, fmt.Errorf("failed to append model: %v", err)
	}
	// Trigger sync and wait for model pull.
	triggerChan <- struct{}{}
	if err := waitForCondition(ctx, func() bool {
		currentState := backendState.Get(ctx)
		data, err := json.Marshal(currentState)
		if err != nil {
			return false
		}
		return bytes.Contains(data, []byte(`"pulledModels":[{"name":"smollm2:135m"`))
	}, 2*time.Minute, 100*time.Millisecond); err != nil {
		for _, fn := range cleanups {
			fn()
		}
		return nil, nil, nil, nil, fmt.Errorf("timeout waiting for condition: %v", err)
	}
	// Return a cleanup function that calls all cleanup functions.
	cleanupAll := func() {
		for _, fn := range cleanups {
			fn()
		}
	}
	return ctx, backendState, dbInstance, cleanupAll, nil
}

func waitForCondition(ctx context.Context, condition func() bool, timeout, interval time.Duration) error {
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
