package localaiapi

import (
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestChatSelectionPreservesDefaultChangedDuringDownload(t *testing.T) {
	ctx := t.Context()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	bus := libbus.NewInMem()
	defer bus.Close()
	state, err := runtimestate.New(ctx, db, bus)
	require.NoError(t, err)
	setup, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, State: state, Bus: bus})
	require.NoError(t, err)
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "native", Name: "Local AI", Type: "modeld", BaseURL: "local"}))
	roles, err := setup.Roles(ctx)
	require.NoError(t, err)
	var pending model
	for _, role := range roles {
		if role.Role == inferencesetup.RoleChat {
			pending = model{Name: "test-model", primaryRevision: role.Revision}
		}
	}
	require.NotEmpty(t, pending.primaryRevision)
	require.NoError(t, clikv.SetString(ctx, store, "default-model", "my-new-choice"))
	h := handler{db: db, setup: setup}
	require.ErrorIs(t, h.useForChat(ctx, pending, "native"), inferencesetup.ErrConfigurationChanged)
	require.Equal(t, "my-new-choice", clikv.Read(ctx, store, "default-model"))
}
