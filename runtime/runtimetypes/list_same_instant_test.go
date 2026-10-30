package runtimetypes_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A freshly created row must be visible to the very next listing. The stores
// page with "created_at < cursor", and SQLite can store second precision, so a
// strict "now" cursor used to hide rows written in the same instant.
func TestUnit_ListBackends_SeesRowsCreatedInTheSameInstant(t *testing.T) {
	ctx := context.TODO()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "same-instant.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := runtimetypes.New(db.WithoutTransaction())

	for i := 0; i < 3; i++ {
		require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{
			ID:   uuid.NewString(),
			Name: fmt.Sprintf("backend-%d", i),
			Type: "ollama",
		}))
	}

	list, err := store.ListBackends(ctx, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 3, "rows created in the same instant as the listing must not vanish")

	// Paginating from the last row still works and does not repeat it.
	if len(list) == 3 {
		page, err := store.ListBackends(ctx, &list[len(list)-1].CreatedAt, 10)
		require.NoError(t, err)
		require.Less(t, len(page), 3, "the cursor must page past rows already returned")
	}
}
