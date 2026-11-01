package runtimestate

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_LoadProviderAPIKey_AmbientAdaptersRunWithoutAStoredRecord(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	for _, backendType := range []string{"bedrock", "vertex-google"} {
		key, err := loadProviderAPIKey(ctx, store, &runtimetypes.Backend{ID: "conn-" + backendType, Name: backendType, Type: backendType})
		require.NoError(t, err, "%s uses the ambient credential chain when nothing is stored, as discovery already does", backendType)
		require.Empty(t, key)
	}

	_, err = loadProviderAPIKey(ctx, store, &runtimetypes.Backend{ID: "conn-openai", Name: "openai", Type: "openai"})
	require.Error(t, err, "an adapter that needs a key is still refused without one")
}
