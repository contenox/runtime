package runtimestate_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func observedState(t *testing.T) (context.Context, *runtimestate.State, runtimetypes.Store, string) {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(dir, "observe.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	return ctx, state, runtimetypes.New(db.WithoutTransaction()), dir
}

func writeScript(t *testing.T, path, model string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"`+model+`","turns":[{"text":"ok"}]}`), 0o600))
}

func TestUnit_ObserversHearOnlyOfAChangedSnapshot(t *testing.T) {
	ctx, state, store, dir := observedState(t)
	_, err := state.Observe(nil)
	require.Error(t, err, "an observer is required")

	var heard atomic.Int32
	stop, err := state.Observe(func() { heard.Add(1) })
	require.NoError(t, err)
	script := filepath.Join(dir, "dialog.json")
	writeScript(t, script, "first")
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "scripted", Name: "scripted", Type: modelrepo.ScriptedTestBackendType, BaseURL: script}))

	require.NoError(t, state.RunProviderCycle(ctx, "scripted"))
	require.Equal(t, int32(1), heard.Load(), "a newly observed connection is a change")
	require.NoError(t, state.RunBackendCycle(ctx))
	require.Equal(t, int32(1), heard.Load(), "an unchanged snapshot is not announced")
	writeScript(t, script, "second")
	require.NoError(t, state.RunBackendCycle(ctx))
	require.Equal(t, int32(2), heard.Load(), "a changed model list is announced")

	stop()
	writeScript(t, script, "third")
	require.NoError(t, state.RunBackendCycle(ctx))
	require.Equal(t, int32(2), heard.Load(), "a removed observer hears nothing")
}

func TestUnit_ScheduledRefreshWaitsWithoutBackgroundDiscovery(t *testing.T) {
	ctx, state, store, dir := observedState(t)
	script := filepath.Join(dir, "dialog.json")
	writeScript(t, script, "only")
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "scripted", Name: "scripted", Type: modelrepo.ScriptedTestBackendType, BaseURL: script}))

	require.NoError(t, state.ScheduleProviderRefresh(ctx, "scripted"))
	observed, ok := state.Get(ctx)["scripted"]
	require.True(t, ok, "without background discovery the refresh has finished on return")
	require.Equal(t, []string{"only"}, observed.Models)
}

func TestUnit_BackgroundDiscoverySchedulesAConnectionSavedElsewhereOnTheNextRead(t *testing.T) {
	ctx, state, store, dir := observedState(t)
	stop, err := state.StartDiscovery(ctx)
	require.NoError(t, err)
	t.Cleanup(stop)
	script := filepath.Join(dir, "dialog.json")
	writeScript(t, script, "late")
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "elsewhere", Name: "elsewhere", Type: modelrepo.ScriptedTestBackendType, BaseURL: script}))

	require.NoError(t, state.ReconcileIfStale(ctx), "a read inside the debounce window")
	require.Eventually(t, func() bool {
		_, ok := state.Get(ctx)["elsewhere"]
		return ok
	}, 10*time.Second, 10*time.Millisecond, "an unobserved connection waits for no debounce")
}

func TestUnit_RecheckIfStaleRunsDiscoveryOlderThanTheDebounce(t *testing.T) {
	original := runtimestate.ReconcileDebounceInterval
	runtimestate.ReconcileDebounceInterval = 50 * time.Millisecond
	t.Cleanup(func() { runtimestate.ReconcileDebounceInterval = original })
	ctx, state, store, dir := observedState(t)
	stop, err := state.StartDiscovery(ctx)
	require.NoError(t, err)
	t.Cleanup(stop)
	script := filepath.Join(dir, "dialog.json")
	writeScript(t, script, "before")
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "scripted", Name: "scripted", Type: modelrepo.ScriptedTestBackendType, BaseURL: script}))
	require.NoError(t, state.RunBackendCycle(ctx))

	writeScript(t, script, "after")
	time.Sleep(2 * runtimestate.ReconcileDebounceInterval)
	require.NoError(t, state.RecheckIfStale(ctx))
	require.Equal(t, []string{"after"}, state.Get(ctx)["scripted"].Models, "the recheck has finished on return")
}
