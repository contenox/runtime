package chatservice

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libbus"
	"github.com/js402/cate/libs/libdb"
	"github.com/js402/cate/libs/libroutine"
	"github.com/js402/cate/libs/libtestenv"
	"github.com/stretchr/testify/require"
)

func SetupTestEnvironment(t *testing.T) (context.Context, *runtimestate.State, libdb.DBManager, func()) {
	ctx := context.TODO()
	err := serverops.NewServiceManager(&serverops.Config{
		JWTExpiry: "1h",
	})
	require.NoError(t, err)
	// We'll collect cleanup functions as we go.
	var cleanups []func()
	addCleanup := func(fn func()) {
		cleanups = append(cleanups, fn)
	}

	// Start local Ollama instance.
	ollamaURI, _, ollamaCleanup, err := libtestenv.SetupLocalInstance(ctx)
	if err != nil {
		t.Fatalf("failed to start local Ollama instance: %v", err)
	}
	addCleanup(ollamaCleanup)

	// Initialize test database.
	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(ctx, uuid.NewString(), "test", "test")
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		t.Fatalf("failed to setup local database: %v", err)
	}
	addCleanup(dbCleanup)

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		t.Fatalf("failed to create new Postgres DB Manager: %v", err)
	}
	ps, cleanup2 := libbus.NewTestPubSub(t)
	addCleanup(cleanup2)

	// Initialize backend service state.
	backendState, err := runtimestate.New(ctx, dbInstance, ps)
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		t.Fatalf("failed to create new backend state: %v", err)
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
		t.Fatalf("failed to create backend: %v", err)
	}

	// Append model to the global model store.
	err = dbStore.AppendModel(ctx, &store.Model{
		Model: "smollm2:135m",
	})
	if err != nil {
		for _, fn := range cleanups {
			fn()
		}
		t.Fatalf("failed to append model: %v", err)
	}

	// Trigger sync and wait for model pull.
	triggerChan <- struct{}{}
	require.Eventually(t, func() bool {
		currentState := backendState.Get(ctx)
		r, err := json.Marshal(currentState)
		if err != nil {
			t.Logf("error marshaling state: %v", err)
			return false
		}
		dst := &bytes.Buffer{}
		if err := json.Compact(dst, r); err != nil {
			t.Logf("error compacting JSON: %v", err)
			return false
		}
		return strings.Contains(string(r), `"pulledModels":[{"name":"smollm2:135m"`)
	}, 2*time.Minute, 100*time.Millisecond)

	// Return a cleanup function that calls all cleanup functions.
	cleanupAll := func() {
		for _, fn := range cleanups {
			fn()
		}
	}
	return ctx, backendState, dbInstance, cleanupAll
}
