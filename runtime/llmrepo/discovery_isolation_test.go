package llmrepo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Discovery_NamedProviderDoesNotWaitForUnrelatedBackend(t *testing.T) {
	ctx, state, db := newReconcileTestState(t, runtimestate.WithAutoDiscoverModels())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var slowCalls atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	defer close(release)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
		if err != nil {
			t.Error(err)
		}
	}))
	defer fast.Close()
	store := runtimetypes.New(db.WithoutTransaction())
	for _, backend := range []*runtimetypes.Backend{
		{ID: "slow", Name: "slow", Type: "openai", BaseURL: slow.URL},
		{ID: "fast", Name: "completion-provider", Type: "openai", BaseURL: fast.URL},
	} {
		require.NoError(t, store.CreateBackend(ctx, backend))
		key, err := json.Marshal(runtimestate.ProviderConfig{APIKey: "test", Type: "openai"})
		require.NoError(t, err)
		require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey("openai", backend.ID), key))
	}
	manager := &modelManager{runtime: state, tracker: libtracker.NoopTracker{}}
	slowDone := make(chan bool, 1)
	go func() { slowDone <- manager.reconcileForResolution(ctx, llmresolver.ErrNoAvailableModels, "slow") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow discovery did not start")
	}
	fastCtx, fastCancel := context.WithTimeout(ctx, time.Second)
	defer fastCancel()
	require.True(t, manager.reconcileForResolution(fastCtx, llmresolver.ErrNoAvailableModels, " Completion-Provider "))
	require.NoError(t, fastCtx.Err())
	snapshot := state.Get(ctx)
	require.Contains(t, snapshot, "fast")
	require.Empty(t, snapshot["fast"].Error)
	require.Equal(t, int32(1), slowCalls.Load())
	cancel()
	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("discovery did not stop on cancellation")
	}
}
