package agentdecl_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/stretchr/testify/require"
)

func TestUnit_LocalRootCanSeedProtectedDeclarations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".contenox")
	previous := vfs.ControlPlaneDenied()
	require.NoError(t, vfs.SetControlPlaneDenied(dir))
	t.Cleanup(func() { require.NoError(t, vfs.SetControlPlaneDenied(previous...)) })

	root := rootOf(t, dir)
	created, err := agentdecl.Preseed(context.Background(), root)
	require.NoError(t, err)
	require.NotEmpty(t, created.Created)
	child, err := root.Child("agents")
	require.NoError(t, err)
	_, err = child.FS.ReadDir(context.Background(), ".")
	require.NoError(t, err)
	view, err := vfs.OpenView(dir)
	require.NoError(t, err)
	_, err = view.Resolve(".")
	require.ErrorIs(t, err, vfs.ErrControlPlane)
	_, err = root.FS.Sub("../outside")
	require.Error(t, err)
}

func rootOf(t *testing.T, dir string) agentdecl.Root {
	t.Helper()
	root, err := agentdecl.LocalRoot(dir)
	require.NoError(t, err)
	return root
}

func rootsOf(t *testing.T, dirs ...string) []agentdecl.Root {
	t.Helper()
	out := make([]agentdecl.Root, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, rootOf(t, dir))
	}
	return out
}

func syncAt(t *testing.T, ctx context.Context, dirs []agentdecl.SourceDir, generated string, cfg agentdecl.Config, opts ...agentdecl.SyncOption) ([]agentdecl.SyncResult, error) {
	t.Helper()
	return agentdecl.Sync(ctx, dirs, rootOf(t, generated), cfg, opts...)
}
