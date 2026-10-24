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

func TestUnit_RunBackendCycle_DeclaredRowDoesNotMakeAChatModelEmbed(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{{
				"id": "mistral-small-fake", "object": "model", "max_context_length": 32768,
				"capabilities": map[string]any{"completion_chat": true, "completion_fim": false},
			}},
		})
	}))
	t.Cleanup(server.Close)

	backend := &runtimetypes.Backend{ID: "mistral-backend", Name: "mistral", Type: "mistral", BaseURL: server.URL}
	require.NoError(t, store.CreateBackend(ctx, backend))
	cred, err := json.Marshal(runtimestate.ProviderConfig{APIKey: "test-key", Type: "mistral"})
	require.NoError(t, err)
	require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey("mistral", backend.ID), cred))
	require.NoError(t, store.AppendModel(ctx, &runtimetypes.Model{ID: "declared", Model: "mistral-small-fake", CanChat: true, CanPrompt: true, CanEmbed: true}))

	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { _ = bus.Close() })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))

	observed := state.Get(ctx)[backend.ID]
	require.Empty(t, observed.Error)
	require.Len(t, observed.PulledModels, 1)
	require.True(t, observed.PulledModels[0].CanChat)
	require.False(t, observed.PulledModels[0].CanEmbed, "only the catalog says a model embeds")
}
