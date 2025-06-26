package store_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/stretchr/testify/require"
)

func TestUnit_Store_QueryingEmptyDB(t *testing.T) {
	ctx := context.TODO()
	connStr, _, cleanup, err := libdb.SetupLocalInstance(ctx, "test", "test", "test")
	require.NoError(t, err)
	dbManager, err := libdb.NewPostgresDBManager(ctx, connStr, store.Schema)
	require.NoError(t, err)
	_ = store.New(dbManager.WithoutTransaction())
	t.Cleanup(func() {
		err := dbManager.Close()
		require.NoError(t, err)

		cleanup()
	})
}
