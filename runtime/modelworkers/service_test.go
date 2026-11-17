package modelworkers_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_WorkerSettings_RemainRepairableWithoutOwner(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "settings.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.primary", "invalid"))
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.admission", "invalid"))
	newClient := func() *modeldconn.Workers {
		client, err := modeldconn.NewConfiguredWorkers(ctx, root, func(ctx context.Context, role string) (modeldconn.WorkerOptions, error) {
			return modelworkers.ReadOptions(ctx, store, role)
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		return client
	}
	first, second := newClient(), newClient()
	service, err := modelworkers.New(db, first)
	require.NoError(t, err)
	save := func(role, memory string) {
		raw, err := json.Marshal(modeldconn.WorkerOptions{Memory: memory})
		require.NoError(t, err)
		require.NoError(t, clikv.SetString(ctx, store, "inference.modeld."+role, string(raw)))
	}
	save("embedding", "2GiB")
	result, err := service.Execute(ctx, modelworkers.Request{Action: "status"})
	require.NoError(t, err)
	require.Equal(t, "unavailable", result.OwnerState)
	require.NotEmpty(t, result.OwnerError)
	require.NotEmpty(t, result.AdmissionError)
	require.NotEmpty(t, result.OptionErrors["primary"])
	require.Equal(t, "2GiB", result.Options["embedding"].Memory)
	save("primary", "1GiB")
	other, err := modelworkers.New(db, second)
	require.NoError(t, err)
	save("embedding", "3GiB")
	_, err = other.Execute(ctx, modelworkers.Request{Action: "status"})
	require.NoError(t, err)
	options, err := first.Options(ctx)
	require.NoError(t, err)
	require.Equal(t, "3GiB", options["embedding"].Memory)
	_, err = service.Execute(ctx, modelworkers.Request{Action: "configure"})
	require.Error(t, err)
	options, err = second.Options(ctx)
	require.NoError(t, err)
	require.Equal(t, "3GiB", options["embedding"].Memory)
}

func TestUnit_WorkerSettings_ExplicitRecoveryInheritanceAndIndependentAssignment(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "inheritance.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.primary", `{"target":{"mode":"cpu"},"memory":"3GiB"}`))
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.alternative", `{"target":{"mode":"accelerator","device_id":"pci:1"},"memory":"2GiB"}`))
	require.NoError(t, clikv.SetString(ctx, store, "inference.assignment.alternative", "inherited"))
	inherited, err := modelworkers.ReadOptions(ctx, store, "alternative")
	require.NoError(t, err)
	primary, err := modelworkers.ReadOptions(ctx, store, "primary")
	require.NoError(t, err)
	require.Equal(t, primary, inherited)
	independent, err := modelworkers.ReadAssignedOptions(ctx, store, "alternative")
	require.NoError(t, err)
	require.NotEqual(t, independent, inherited)
	require.NoError(t, clikv.SetString(ctx, store, "inference.assignment.alternative", "assigned"))
	assigned, err := modelworkers.ReadOptions(ctx, runtimetypes.New(db.WithoutTransaction()), "alternative")
	require.NoError(t, err)
	require.Equal(t, independent, assigned)
}
