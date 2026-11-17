package inferencesetup_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestIntegration_IndependentCloudSetup_IgnoresBrokenLocalSettingsAndOtherConnections(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "independent.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	store := runtimetypes.New(db.WithoutTransaction())
	var otherCalls, chatCalls atomic.Int32
	var editDuringVerification atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(other.Close)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cloud-key" {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch r.URL.Path {
		case "/models":
			response = map[string]any{"data": []map[string]any{{"id": "cloud-chat", "capabilities": []string{"chat"}}}}
		case "/chat/completions":
			chatCalls.Add(1)
			if editDuringVerification.Swap(false) {
				if err := clikv.SetString(r.Context(), store, "default-model", "concurrent-selection"); err != nil {
					t.Error(err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			response = toolProbeHTTPResponse(t, r)
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(cloud.Close)
	for _, backend := range []*runtimetypes.Backend{
		{ID: "broken-local", Name: "broken-local", Type: "modeld", BaseURL: other.URL},
		{ID: "other-cloud", Name: "other-cloud", Type: "openai", BaseURL: other.URL},
	} {
		require.NoError(t, store.CreateBackend(ctx, backend))
	}
	for _, key := range []string{"inference.modeld.primary", "inference.modeld.autocomplete", "inference.modeld.embedding", "inference.modeld.admission"} {
		require.NoError(t, clikv.SetString(ctx, store, key, "invalid settings"))
	}
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	newService := func(state *runtimestate.State) *inferencesetup.Service {
		service, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
		require.NoError(t, err)
		return service
	}
	service := newService(state)
	configuration, err := service.Configuration(ctx)
	require.NoError(t, err)
	require.Len(t, configuration.Roles, 5)
	nextEdit, err := service.VerifiedRole(ctx, inferencesetup.RoleNextEdit)
	require.Error(t, err)
	require.False(t, nextEdit.Enabled)
	require.Len(t, configuration.Providers, 2)
	require.NotEmpty(t, configuration.ProviderTypes)
	require.Empty(t, configuration.Models)
	require.Zero(t, otherCalls.Load())
	require.NoError(t, service.ConfigureProvider(ctx, inferencesetup.ProviderRequest{Name: "chosen-cloud", Provider: "openai", Endpoint: cloud.URL, APIKey: "cloud-key"}))
	require.Zero(t, otherCalls.Load())
	backend, err := store.GetBackendByName(ctx, "chosen-cloud")
	require.NoError(t, err)
	selector := runtimestate.ConnectionSelector(backend.ID)
	_, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: selector, Model: "cloud-chat"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, int32(2), chatCalls.Load())
	ready, err := service.Ready(ctx)
	require.NoError(t, err)
	require.False(t, ready)
	role, err := service.VerifiedRole(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	require.Equal(t, "cloud-chat", role.Model)
	require.Equal(t, selector, role.Provider)
	var competingCalls atomic.Int32
	competing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer second-key" {
			http.Error(w, "wrong connection credentials", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/models" {
			competingCalls.Add(1)
			http.Error(w, "wrong connection selected", http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "cloud-chat"}}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(competing.Close)
	second, err := service.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "second-account", Provider: "openai", Endpoint: competing.URL, APIKey: "second-key"})
	require.NoError(t, err)
	require.Empty(t, second.DiscoveryError)
	configuration, err = service.Configuration(ctx)
	require.NoError(t, err)
	require.Len(t, configuration.Models, 2)
	require.NotEqual(t, configuration.Models[0].Provider, configuration.Models[1].Provider)
	_, err = service.VerifiedRole(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	_, err = service.Verify(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	require.Equal(t, int32(4), chatCalls.Load())
	require.Zero(t, competingCalls.Load())
	require.NoError(t, service.RemoveProvider(ctx, second.ID))
	_, err = service.VerifiedRole(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	require.NoError(t, service.RefreshProvider(ctx, backend.ID))
	require.Zero(t, otherCalls.Load())
	restartedState, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, restartedState.RunProviderCycle(ctx, backend.ID))
	restarted := newService(restartedState)
	_, err = restarted.VerifiedRole(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	_, err = restarted.Verify(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	require.Equal(t, int32(6), chatCalls.Load())
	require.Zero(t, otherCalls.Load())
	editDuringVerification.Store(true)
	_, err = restarted.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: selector, Model: "cloud-chat"}, inferencesetup.SourceUser)
	require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
	var selected string
	require.NoError(t, store.GetKV(ctx, clikv.Prefix+"default-model", &selected))
	require.Equal(t, "concurrent-selection", selected)
}
