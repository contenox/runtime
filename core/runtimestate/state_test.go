package runtimestate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libbus"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/contenox/contenox/libs/libroutine"
	"github.com/contenox/contenox/libs/libtestenv"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStateLogic(t *testing.T) {
	// if os.Getenv("SMOKETESTS") == "" {
	// 	t.Skip("Set env SMOKETESTS to true to run this test")
	// }
	ctx := context.TODO()
	uri, _, cleanup, err := libtestenv.SetupOllamaLocalInstance(ctx)
	require.NoError(t, err)
	defer cleanup()

	//defer cancel()

	dbConn, _, cleanupDB, err := libdb.SetupLocalInstance(ctx, "test", "test", "test")
	require.NoError(t, err)
	defer cleanupDB()

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	require.NoError(t, err)

	dbStore := store.New(dbInstance.WithoutTransaction())

	// Create backend first
	backendID := uuid.NewString()
	err = dbStore.CreateBackend(ctx, &store.Backend{
		ID:      backendID,
		Name:    "myLLama",
		BaseURL: uri,
		Type:    "Ollama",
	})
	require.NoError(t, err)

	// Append model to the global model store
	err = dbStore.AppendModel(ctx, &store.Model{
		Model: "granite-embedding:30m",
	})
	require.NoError(t, err)
	ps, cleanup2, err := libbus.NewTestPubSub()
	defer cleanup2()
	if err != nil {
		t.Fatal(err)
	}

	backendState, err := runtimestate.New(ctx, dbInstance, ps)
	require.NoError(t, err)
	triggerChan := make(chan struct{}, 10)

	// Create a breaker instance with an example threshold and reset timeout.
	breaker := libroutine.NewRoutine(3, 10*time.Second)
	// Instead of calling Run on the state service, we loop using RunCycle.
	go breaker.Loop(ctx, time.Second, triggerChan, backendState.RunBackendCycle, func(err error) {})
	breaker2 := libroutine.NewRoutine(3, 10*time.Second)
	go breaker2.Loop(ctx, time.Second, triggerChan, backendState.RunDownloadCycle, func(err error) {})

	// Initial state check: it should be empty since sync hasn't occurred yet.
	state := backendState.Get(ctx)
	require.Len(t, state, 0)

	// Trigger sync and verify state
	triggerChan <- struct{}{}
	require.Eventually(t, func() bool {
		state = backendState.Get(ctx)
		return len(state) == 1
	}, 2*time.Second, 100*time.Millisecond)

	// Verify state contents
	state = backendState.Get(ctx)
	r, err := json.Marshal(state)
	require.NoError(t, err)

	dst := &bytes.Buffer{}
	err = json.Compact(dst, []byte(r))
	require.NoError(t, err)

	stateMsg := dst.String()
	require.Contains(t, stateMsg, `"name":"myLLama"`)
	require.Contains(t, stateMsg, `"models":["granite-embedding:30m"]`)

	// // Verify queue processing: ensure that there is no item in progress.
	// require.Eventually(t, func() bool {
	// 	current := backendState.InPorgressQueueState()
	// 	return current == nil
	// }, 20*time.Second, 100*time.Millisecond)

	// Trigger final sync and verify model pull
	triggerChan <- struct{}{}
	require.Eventually(t, func() bool {
		currentState := backendState.Get(ctx)
		r, _ := json.Marshal(currentState)
		return strings.Contains(string(r), `"pulledModels":[{"name":"granite-embedding:30m"`)
	}, 30*time.Second, 100*time.Millisecond)
}

func TestBackendDeletion(t *testing.T) {
	// if os.Getenv("SMOKETESTS") == "" {
	// 	t.Skip("Set env SMOKETESTS to true to run this test")
	// }
	ctx := context.TODO()
	uri, _, cleanup, err := libtestenv.SetupOllamaLocalInstance(ctx)
	require.NoError(t, err)
	defer cleanup()

	dbConn, _, cleanupDB, err := libdb.SetupLocalInstance(ctx, "test", "test", "test")
	require.NoError(t, err)
	defer cleanupDB()

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	require.NoError(t, err)

	dbStore := store.New(dbInstance.WithoutTransaction())
	ps, cleanup2, err := libbus.NewTestPubSub()
	defer cleanup2()
	require.NoError(t, err)

	backendState, err := runtimestate.New(ctx, dbInstance, ps)
	require.NoError(t, err)
	triggerChan := make(chan struct{}, 10)

	breaker := libroutine.NewRoutine(3, 10*time.Second)
	go breaker.Loop(ctx, time.Second, triggerChan, backendState.RunBackendCycle, func(err error) {})
	breaker2 := libroutine.NewRoutine(3, 10*time.Second)
	go breaker2.Loop(ctx, time.Second, triggerChan, backendState.RunDownloadCycle, func(err error) {})

	// Create backend
	backendID := uuid.NewString()
	require.NoError(t, dbStore.CreateBackend(ctx, &store.Backend{
		ID:      backendID,
		Name:    "test-backend",
		BaseURL: uri,
		Type:    "Ollama",
	}))

	// Append model
	require.NoError(t, dbStore.AppendModel(ctx, &store.Model{
		Model: "granite-embedding:30m",
	}))

	// Verify creation
	triggerChan <- struct{}{}
	require.Eventually(t, func() bool {
		return len(backendState.Get(ctx)) == 1
	}, 2*time.Second, 100*time.Millisecond)

	// Delete backend
	require.NoError(t, dbStore.DeleteBackend(ctx, backendID))
	triggerChan <- struct{}{}

	// Verify deletion
	require.Eventually(t, func() bool {
		return len(backendState.Get(ctx)) == 0
	}, 2*time.Second, 100*time.Millisecond)
}

func TestPoolBasedModelAssignment(t *testing.T) {
	// if os.Getenv("SMOKETESTS") == "" {
	// 	t.Skip("Set env SMOKETESTS to true to run this test")
	// }
	ctx := context.TODO()
	uri, _, cleanup, err := libtestenv.SetupOllamaLocalInstance(ctx)
	require.NoError(t, err)
	defer cleanup()

	dbConn, _, cleanupDB, err := libdb.SetupLocalInstance(ctx, "test", "test", "test")
	require.NoError(t, err)
	defer cleanupDB()

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	require.NoError(t, err)

	dbStore := store.New(dbInstance.WithoutTransaction())
	ps, cleanup2, err := libbus.NewTestPubSub()
	defer cleanup2()
	require.NoError(t, err)

	// Create backend and pool
	backendID := uuid.NewString()
	poolID := uuid.NewString()

	// 1. Create pool
	err = dbStore.CreatePool(ctx, &store.Pool{
		ID:          poolID,
		Name:        "embed-pool",
		PurposeType: "embed",
	})
	require.NoError(t, err)

	// 2. Create backend and assign to pool
	err = dbStore.CreateBackend(ctx, &store.Backend{
		ID:      backendID,
		Name:    "embed-backend",
		BaseURL: uri,
		Type:    "Ollama",
	})
	require.NoError(t, err)
	err = dbStore.AssignBackendToPool(ctx, poolID, backendID)
	require.NoError(t, err)

	// 3. Create model and assign to pool
	modelName := "granite-embedding:30m"
	modelID := uuid.NewString()
	err = dbStore.AppendModel(ctx, &store.Model{
		ID:    modelID,
		Model: modelName,
	})
	require.NoError(t, err)
	err = dbStore.AssignModelToPool(ctx, poolID, modelID)
	require.NoError(t, err)

	// Initialize state with pool support
	backendState, err := runtimestate.New(ctx, dbInstance, ps, runtimestate.WithPools())
	require.NoError(t, err)
	triggerChan := make(chan struct{}, 10)

	breaker := libroutine.NewRoutine(3, 10*time.Second)
	go breaker.Loop(ctx, time.Second, triggerChan, backendState.RunBackendCycle, func(err error) {})
	breaker2 := libroutine.NewRoutine(3, 10*time.Second)
	go breaker2.Loop(ctx, time.Second, triggerChan, backendState.RunDownloadCycle, func(err error) {})

	// Trigger sync and verify model download
	triggerChan <- struct{}{}
	require.Eventually(t, func() bool {
		state := backendState.Get(ctx)
		if len(state) == 0 {
			return false
		}
		backendState := state[backendID]
		for _, model := range backendState.PulledModels {
			if model.Name == modelName {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond)
}
