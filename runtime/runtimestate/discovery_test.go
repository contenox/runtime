package runtimestate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Discovery_BackgroundPublishesFastBackendAndStops(t *testing.T) {
	ctx, state, db := newReconcileStateTest(t, WithAutoDiscoverModels())
	slowStarted := make(chan struct{}, 1)
	var slowCalls atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowCalls.Add(1)
		select {
		case slowStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer slow.Close()
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
		{ID: "fast", Name: "fast", Type: "openai", BaseURL: fast.URL},
	} {
		require.NoError(t, store.CreateBackend(ctx, backend))
		key, err := json.Marshal(ProviderConfig{APIKey: "test", Type: "openai"})
		require.NoError(t, err)
		require.NoError(t, store.SetKV(ctx, BackendCredentialKey("openai", backend.ID), key))
	}
	stop, err := state.StartDiscovery(ctx)
	require.NoError(t, err)
	defer stop()
	select {
	case <-slowStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("slow backend was not probed")
	}
	require.Eventually(t, func() bool {
		snapshot := state.Get(ctx)
		fast, exists := snapshot["fast"]
		return exists && len(fast.PulledModels) == 1
	}, 10*time.Second, 5*time.Millisecond)
	for range 5 {
		require.NoError(t, state.ReconcileIfStale(ctx))
	}
	waitCtx, cancel := context.WithCancel(ctx)
	waitDone := make(chan error, 1)
	go func() { waitDone <- state.RunProviderCycle(waitCtx, "slow") }()
	cancel()
	select {
	case err := <-waitDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter blocked")
	}
	require.Equal(t, int32(1), slowCalls.Load())
	require.Contains(t, state.Get(ctx), "fast")
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel and join discovery")
	}
}

func TestUnit_Discovery_ProviderCyclePreservesUnrelatedSnapshots(t *testing.T) {
	ctx, state, db := newReconcileStateTest(t)
	state.state.Store("other", &BackendRuntimeState{ID: "other", Backend: runtimetypes.Backend{ID: "other", Name: "other", Type: "openai"}})
	state.state.Store("removed", &BackendRuntimeState{ID: "removed", Backend: runtimetypes.Backend{ID: "removed", Name: "target", Type: "openai"}})
	require.NoError(t, state.RunProviderCycle(ctx, "target"))
	require.Contains(t, state.Get(ctx), "other")
	require.NotContains(t, state.Get(ctx), "removed")
	store := runtimetypes.New(db.WithoutTransaction())
	backend := &runtimetypes.Backend{ID: "renamed", Name: "old-name", Type: "unsupported", BaseURL: "http://unused"}
	require.NoError(t, store.CreateBackend(ctx, backend))
	require.NoError(t, state.RunProviderCycle(ctx, "old-name"))
	require.Contains(t, state.Get(ctx), "renamed")
	backend.Name = "new-name"
	require.NoError(t, store.UpdateBackend(ctx, backend))
	require.NoError(t, state.RunProviderCycle(ctx, "old-name"))
	require.NotContains(t, state.Get(ctx), "renamed")
	require.NoError(t, state.RunProviderCycle(ctx, "new-name"))
	require.Equal(t, "new-name", state.Get(ctx)["renamed"].Backend.Name)
}

func TestUnit_Discovery_GroupMembershipAndModelUnion(t *testing.T) {
	ctx, state, db := newReconcileStateTest(t, WithGroups())
	store := runtimetypes.New(db.WithoutTransaction())
	for _, id := range []string{"member", "excluded"} {
		require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: id, Name: id, Type: "unsupported", BaseURL: "http://unused"}))
	}
	for _, id := range []string{"first", "second"} {
		require.NoError(t, store.CreateAffinityGroup(ctx, &runtimetypes.AffinityGroup{ID: id, Name: id, PurposeType: "chat"}))
		require.NoError(t, store.AssignBackendToAffinityGroup(ctx, id, "member"))
		require.NoError(t, store.AppendModel(ctx, &runtimetypes.Model{ID: id, Model: id, CanChat: true}))
		require.NoError(t, store.AssignModelToAffinityGroup(ctx, id, id))
	}
	job, err := state.currentBackendJob(ctx, "member")
	require.NoError(t, err)
	names := make([]string, 0, len(job.models))
	for _, model := range job.models {
		names = append(names, model.Model)
	}
	require.ElementsMatch(t, []string{"first", "second"}, names)
	require.NoError(t, state.RunProviderCycle(ctx, "member"))
	require.Contains(t, state.Get(ctx), "member")
	require.NoError(t, state.RunProviderCycle(ctx, "excluded"))
	require.NotContains(t, state.Get(ctx), "excluded")
	require.Contains(t, state.Get(ctx), "member")
}
