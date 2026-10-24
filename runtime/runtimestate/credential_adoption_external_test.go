package runtimestate_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func adoptionStore(t *testing.T) (context.Context, runtimetypes.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite+libkvstore.SQLiteSchema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, runtimetypes.New(db.WithoutTransaction())
}

func setSharedRow(t *testing.T, ctx context.Context, store runtimetypes.Store, adapterType string, record runtimestate.ProviderConfig) {
	t.Helper()
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, store.SetKV(ctx, runtimestate.ProviderKeyPrefix+adapterType, raw))
}

func ownRecord(t *testing.T, ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend) (runtimestate.ProviderConfig, bool) {
	t.Helper()
	var record runtimestate.ProviderConfig
	err := store.GetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), &record)
	if err != nil {
		require.ErrorIs(t, err, libdb.ErrNotFound)
		return runtimestate.ProviderConfig{}, false
	}
	return record, true
}

func TestUnit_AdoptSharedCredentialRows_MovesEachRowIntoTheRecordlessConnectionsOfItsTypeOnce(t *testing.T) {
	ctx, store := adoptionStore(t)
	recordless := &runtimetypes.Backend{ID: "openai-without-record", Name: "b", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"}
	keyed := &runtimetypes.Backend{ID: "openai-with-record", Name: "a", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"}
	vllm := &runtimetypes.Backend{ID: "vllm-without-record", Name: "v", Type: "vllm", BaseURL: "http://127.0.0.1:1"}
	gateway := &runtimetypes.Backend{ID: "gateway-without-record", Name: "contenox", Type: "ollama", BaseURL: "http://127.0.0.1:1"}
	for _, backend := range []*runtimetypes.Backend{recordless, keyed, vllm, gateway} {
		require.NoError(t, store.CreateBackend(ctx, backend))
	}
	own, err := json.Marshal(runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: "OWN_KEY"})
	require.NoError(t, err)
	require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey(keyed.Type, keyed.ID), own))
	setSharedRow(t, ctx, store, "openai", runtimestate.ProviderConfig{Type: "openai", APIKey: "sk-shared-literal", APIKeyEnv: "SHARED_VAR"})
	setSharedRow(t, ctx, store, "ollama", runtimestate.ProviderConfig{Type: "ollama", APIKey: "gw-shared-key"})
	setSharedRow(t, ctx, store, "anthropic", runtimestate.ProviderConfig{Type: "anthropic"})

	require.NoError(t, runtimestate.AdoptSharedCredentialRows(ctx, store))

	adopted, ok := ownRecord(t, ctx, store, recordless)
	require.True(t, ok, "the record-less openai connection adopts the shared row")
	require.Equal(t, runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: "SHARED_VAR"}, adopted, "the variable reference is adopted and the literal beside it is dropped")
	kept, ok := ownRecord(t, ctx, store, keyed)
	require.True(t, ok)
	require.Equal(t, runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: "OWN_KEY"}, kept, "a connection with its own record keeps it")
	_, ok = ownRecord(t, ctx, store, vllm)
	require.False(t, ok, "a connection of another type adopts nothing")
	gatewayRecord, ok := ownRecord(t, ctx, store, gateway)
	require.True(t, ok)
	require.Equal(t, runtimestate.ProviderConfig{Type: "ollama", CredentialSource: runtimestate.CredentialStored, APIKey: "gw-shared-key"}, gatewayRecord)

	rows, err := store.ListKVPrefix(ctx, runtimestate.ProviderKeyPrefix, nil, runtimetypes.MAXLIMIT)
	require.NoError(t, err)
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.Key)
	}
	require.ElementsMatch(t, []string{
		runtimestate.BackendCredentialKey(recordless.Type, recordless.ID),
		runtimestate.BackendCredentialKey(keyed.Type, keyed.ID),
		runtimestate.BackendCredentialKey(gateway.Type, gateway.ID),
	}, keys, "every shared row is gone, including the one that held no credential")

	later := &runtimetypes.Backend{ID: "openai-added-later", Name: "c", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"}
	require.NoError(t, store.CreateBackend(ctx, later))
	require.NoError(t, runtimestate.AdoptSharedCredentialRows(ctx, store))
	_, ok = ownRecord(t, ctx, store, later)
	require.False(t, ok, "a connection saved after the adoption inherits nothing")
}
