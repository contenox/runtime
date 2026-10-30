package runtimetypes_test

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Store_QueryingEmptyDB(t *testing.T) {
	ctx := context.TODO()
	// The store is SQL, not Postgres-specific; SQLite keeps this test runnable
	// everywhere and is the database the desktop app ships.
	dbManager, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "store.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	_ = runtimetypes.New(dbManager.WithoutTransaction())
	t.Cleanup(func() {
		require.NoError(t, dbManager.Close())
	})
}
