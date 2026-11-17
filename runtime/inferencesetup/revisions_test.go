package inferencesetup_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
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

func TestIntegration_Roles_VerifyUsesCurrentCredentialWithoutDiscovery(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "setup.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	var expected atomic.Value
	expected.Store("Bearer original-test-key")
	var discoveryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body string
		if r.URL.Path == "/models" {
			discoveryCalls.Add(1)
			body = `{"data":[{"id":"chat-model","capabilities":["chat"]}]}`
		} else {
			if r.Header.Get("Authorization") != expected.Load().(string) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			raw, err := json.Marshal(toolProbeHTTPResponse(t, r))
			if err != nil {
				t.Error(err)
				return
			}
			body = string(raw)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	service, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
	require.NoError(t, err)
	connection, err := service.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "account", Provider: "openai", Endpoint: server.URL, APIKey: "original-test-key"})
	require.NoError(t, err)
	assignment := inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: runtimestate.ConnectionSelector(connection.ID), Model: "chat-model"}
	_, err = service.Assign(ctx, assignment, inferencesetup.SourceUser)
	require.NoError(t, err)
	calls := discoveryCalls.Load()
	expected.Store("Bearer replacement-test-key")
	raw, err := json.Marshal(runtimestate.ProviderConfig{Type: "openai", APIKey: "replacement-test-key"})
	require.NoError(t, err)
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).SetKV(ctx, runtimestate.BackendCredentialKey("openai", connection.ID), raw))
	_, err = service.Verify(ctx, inferencesetup.RoleChat)
	require.NoError(t, err)
	require.Equal(t, calls, discoveryCalls.Load())
}

func TestIntegration_Roles_ConcurrentEditsRejectVerification(t *testing.T) {
	for _, edit := range []string{"credential", "role-aba", "inputs-aba", "endpoint-aba", "cancel"} {
		t.Run(edit, func(t *testing.T) {
			ctx := context.Background()
			db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "setup.db"), runtimetypes.SchemaSQLite)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			bus := libbus.NewSQLite(db.WithoutTransaction())
			t.Cleanup(func() { require.NoError(t, bus.Close()) })
			store := runtimetypes.New(db.WithoutTransaction())
			var mu sync.Mutex
			mutate := func() {}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/models" {
					_, err := w.Write([]byte(`{"data":[{"id":"chat-model","capabilities":["chat"]}]}`))
					if err != nil {
						t.Error(err)
					}
					return
				}
				mu.Lock()
				mutate()
				mu.Unlock()
				err := json.NewEncoder(w).Encode(toolProbeHTTPResponse(t, r))
				if err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
			require.NoError(t, err)
			service, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
			require.NoError(t, err)
			connection, err := service.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "account", Provider: "openai", Endpoint: server.URL, APIKey: "original-test-key"})
			require.NoError(t, err)
			assignment := inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: runtimestate.ConnectionSelector(connection.ID), Model: "chat-model"}
			original, err := service.Assign(ctx, assignment, inferencesetup.SourceUser)
			require.NoError(t, err)
			verifyCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			mu.Lock()
			mutate = func() {
				var err error
				switch edit {
				case "credential":
					raw, marshalErr := json.Marshal(runtimestate.ProviderConfig{Type: "openai", APIKey: "replacement-test-key"})
					if marshalErr != nil {
						t.Error(marshalErr)
						return
					}
					err = store.SetKV(ctx, runtimestate.BackendCredentialKey("openai", connection.ID), raw)
				case "role-aba":
					err = clikv.SetString(ctx, store, "default-model", "other-model")
					if err == nil {
						err = clikv.SetString(ctx, store, "default-model", "chat-model")
					}
				case "inputs-aba":
					err = clikv.SetString(ctx, store, "inference.inputs.primary", "images")
					if err == nil {
						err = clikv.SetString(ctx, store, "inference.inputs.primary", "text")
					}
				case "endpoint-aba":
					var backend *runtimetypes.Backend
					backend, err = store.GetBackend(ctx, connection.ID)
					if err == nil {
						backend.BaseURL = "http://changed.invalid"
						err = store.UpdateBackend(ctx, backend)
					}
					if err == nil {
						backend.BaseURL = server.URL
						err = store.UpdateBackend(ctx, backend)
					}
				case "cancel":
					cancel()
				}
				if err != nil {
					t.Error(err)
				}
			}
			mu.Unlock()
			_, err = service.Verify(verifyCtx, inferencesetup.RoleChat)
			if edit == "cancel" {
				require.Error(t, err)
			} else {
				require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
			}
			var persisted inferencesetup.Verification
			require.NoError(t, store.GetKV(ctx, "inference.setup.verification.primary", &persisted))
			require.Equal(t, original, &persisted)
		})
	}
}
