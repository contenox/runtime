package contenoxcli

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_LiveDefaultsSharedAcrossInstancesAndNoHardcodedModel(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "shared.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Two runtime instances (default workspace + project marker workspace)
	// resolve through the same shared DB with no pinned flags.
	defaultInst := liveDefaultsResolver(db, false, acpsvc.LiveDefaults{})
	projectInst := liveDefaultsResolver(db, false, acpsvc.LiveDefaults{})

	require.Empty(t, defaultInst(ctx).Model, "no hardcoded model fallback")
	require.Empty(t, projectInst(ctx).Model)

	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, clikv.SetString(ctx, store, "default-model", "gpt-test"))
	require.NoError(t, clikv.SetString(ctx, store, "default-provider", "openai-codex"))

	for _, r := range []func(context.Context) acpsvc.LiveDefaults{defaultInst, projectInst} {
		d := r(ctx)
		require.Equal(t, "gpt-test", d.Model)
		require.Equal(t, "openai-codex", d.Provider)
	}
	// Workspace-scoped key read for a project workspace still falls back to global.
	v, src := clikv.ReadConfig(ctx, store, "11111111-1111-1111-1111-111111111111", "default-model")
	require.Equal(t, "gpt-test", v)
	require.Equal(t, "global", src)
}
