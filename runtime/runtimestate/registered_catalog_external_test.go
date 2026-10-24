package runtimestate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_RunBackendCycle_ObservesEveryRegisteredCatalog(t *testing.T) {
	for _, backendType := range []string{"deepseek", "mistral"} {
		t.Run(backendType, func(t *testing.T) {
			ctx := context.Background()
			db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			store := runtimetypes.New(db.WithoutTransaction())

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"object": "list",
					"data":   []map[string]any{{"id": backendType + "-served", "object": "model"}},
				})
			}))
			t.Cleanup(server.Close)

			backend := &runtimetypes.Backend{ID: backendType + "-backend", Name: backendType, Type: backendType, BaseURL: server.URL}
			require.NoError(t, store.CreateBackend(ctx, backend))
			cred, err := json.Marshal(runtimestate.ProviderConfig{APIKey: "test-key", Type: backendType})
			require.NoError(t, err)
			require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey(backendType, backend.ID), cred))

			bus := libbus.NewSQLite(db.WithoutTransaction())
			t.Cleanup(func() { _ = bus.Close() })
			state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
			require.NoError(t, err)
			require.NoError(t, state.RunBackendCycle(ctx))

			observed := state.Get(ctx)[backend.ID]
			require.Empty(t, observed.Error, "a backend type with a registered catalog must be observed, not refused")
			require.NotEmpty(t, observed.PulledModels)
			require.Equal(t, backendType+"-served", observed.PulledModels[0].Model)
		})
	}
}
