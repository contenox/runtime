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
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestIntegration_Registration_RoutesDuplicateNamesWithoutDiscoveryAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "registered.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	newService := func() *inferencesetup.Service {
		state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
		require.NoError(t, err)
		service, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
		require.NoError(t, err)
		return service
	}
	service := newService()
	var discovery, firstCalls, secondCalls atomic.Int32
	endpoint := func(key string, calls *atomic.Int32) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				http.Error(w, "wrong connection credentials", http.StatusUnauthorized)
				return
			}
			if r.URL.Path == "/models" {
				discovery.Add(1)
				http.Error(w, "catalog disabled", http.StatusNotFound)
				return
			}
			if r.URL.Path != "/chat/completions" {
				http.NotFound(w, r)
				return
			}
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(toolProbeHTTPResponse(t, r)); err != nil {
				t.Error(err)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}
	first := endpoint("first-secret", &firstCalls)
	second := endpoint("second-secret", &secondCalls)
	connections := []string{}
	for index, server := range []*httptest.Server{first, second} {
		name, key := "first", "first-secret"
		if index == 1 {
			name, key = "second", "second-secret"
		}
		saved, err := service.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: name, Provider: "openai", Endpoint: server.URL, CredentialSource: "stored", APIKey: key})
		require.NoError(t, err)
		require.NotEmpty(t, saved.DiscoveryError)
		connections = append(connections, saved.ID)
		require.NoError(t, service.RegisterModel(ctx, inferencesetup.RegisterModelRequest{ConnectionID: saved.ID, Model: "same-model", Facts: runtimetypes.ModelFacts{Registered: true, ContextLength: 8192, Capabilities: []string{"completion", "tools"}, CompletionMode: "instruct"}}))
	}
	config, err := service.Configuration(ctx)
	require.NoError(t, err)
	require.Len(t, config.Models, 2)
	for _, model := range config.Models {
		require.True(t, model.Registered)
		require.Empty(t, model.AdvertisedCapabilities)
		require.Equal(t, []string{"completion", "tools"}, model.DeclaredCapabilities)
		// The adapter's capability list for a registered model is what it
		// executes for that model's declaration: an operation the declaration
		// does not name is not claimed on its behalf.
		require.Contains(t, model.AdapterCapabilities, "completion")
		require.NotContains(t, model.AdapterCapabilities, "vision")
		require.Equal(t, "instruct", model.CompletionMode)
	}
	for _, connection := range config.Providers {
		require.Equal(t, "failed", connection.DiscoveryState)
		require.Equal(t, "stored", connection.CredentialSource)
		require.Equal(t, "unverified", connection.AuthorizationState)
	}
	observed := discovery.Load()
	for index, role := range []string{inferencesetup.RoleChat, inferencesetup.RoleRecovery} {
		_, err := service.Assign(ctx, inferencesetup.Assignment{Role: role, Provider: runtimestate.ConnectionSelector(connections[index]), Model: "same-model"}, inferencesetup.SourceUser)
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), firstCalls.Load())
	require.Equal(t, int32(2), secondCalls.Load())
	service = newService()
	for _, role := range []string{inferencesetup.RoleChat, inferencesetup.RoleRecovery} {
		_, err := service.VerifiedRole(ctx, role)
		require.NoError(t, err)
		_, err = service.Verify(ctx, role)
		require.NoError(t, err)
	}
	require.Equal(t, int32(4), firstCalls.Load())
	require.Equal(t, int32(4), secondCalls.Load())
	require.Equal(t, observed, discovery.Load())
	err = service.RegisterModel(ctx, inferencesetup.RegisterModelRequest{ConnectionID: connections[1], Model: "unsupported", Facts: runtimetypes.ModelFacts{Registered: true, ContextLength: 8192, Capabilities: []string{"audio"}, AudioExtension: true}})
	require.ErrorContains(t, err, "inline audio extension applies only to Ollama")
	_, err = service.VerifiedRole(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	_, err = service.VerifiedRole(ctx, inferencesetup.RoleRecovery)
	require.NoError(t, err)
	config, err = service.Configuration(ctx)
	require.NoError(t, err)
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "first-secret")
	require.NotContains(t, string(raw), "second-secret")
	for _, role := range config.Roles {
		if role.Role == inferencesetup.RoleChat || role.Role == inferencesetup.RoleRecovery {
			require.Equal(t, "assigned", role.AssignmentState)
			require.NotEmpty(t, role.ConnectionID)
		}
	}
}
