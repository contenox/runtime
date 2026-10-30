package modelregistryservice_test

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func setupServiceDB(t *testing.T) (context.Context, libdb.DBManager) {
	t.Helper()

	ctx := context.Background()
	// The registry is SQL, not Postgres-specific; SQLite keeps this test
	// runnable everywhere and is the database the desktop app ships.
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "modelregistry.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	return ctx, db
}

func TestUnit_ModelRegistryService_CreateValidatesEmptyName(t *testing.T) {
	svc := modelregistryservice.New(nil)
	err := svc.Create(context.Background(), &runtimetypes.ModelRegistryEntry{
		Name:      "",
		SourceURL: "https://example.com/model.gguf",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, modelregistryservice.ErrInvalidEntry)
}

func TestUnit_ModelRegistryService_CreateValidatesEmptySourceURL(t *testing.T) {
	svc := modelregistryservice.New(nil)
	err := svc.Create(context.Background(), &runtimetypes.ModelRegistryEntry{
		Name:      "my-model",
		SourceURL: "",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, modelregistryservice.ErrInvalidEntry)
}

func TestUnit_ModelRegistryService_CreateAndGet(t *testing.T) {
	ctx, db := setupServiceDB(t)
	svc := modelregistryservice.New(db)

	e := &runtimetypes.ModelRegistryEntry{
		ID:        uuid.NewString(),
		Name:      "svc-test-model",
		SourceURL: "https://huggingface.co/test/model.gguf",
		SizeBytes: 100_000_000,
	}
	require.NoError(t, svc.Create(ctx, e))
	require.NotEmpty(t, e.ID)

	got, err := svc.Get(ctx, e.ID)
	require.NoError(t, err)
	require.Equal(t, e.Name, got.Name)
	require.Equal(t, e.SourceURL, got.SourceURL)
}

func TestUnit_ModelRegistryService_Delete(t *testing.T) {
	ctx, db := setupServiceDB(t)
	svc := modelregistryservice.New(db)

	e := &runtimetypes.ModelRegistryEntry{
		ID:        uuid.NewString(),
		Name:      "to-delete",
		SourceURL: "https://huggingface.co/del/model.gguf",
	}
	require.NoError(t, svc.Create(ctx, e))
	require.NoError(t, svc.Delete(ctx, e.ID))

	_, err := svc.Get(ctx, e.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}
