package agentdecl_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/require"
)

func TestUnit_Sync_ResolvedRootKeySurvivesASymlinkedPath(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	declare(t, filepath.Join(root, agentdecl.NativeSourceDir), "triage.md", declTriage)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("cannot create a symlink on this host: %v", err)
	}

	generated := filepath.Join(root, agentdecl.GeneratedDirName)

	dirs := agentdecl.DiscoverSourceDirs(ctx, rootsOf(t, link), nil)
	results, err := syncAt(t, ctx, dirs, generated, mustConfig(t))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, agentdecl.ActionCreated, results[0].Action)
	require.Equal(t, filepath.Join(root, agentdecl.NativeSourceDir, "triage.md"), results[0].Source,
		"a source is named by its resolved path")

	dirs = agentdecl.DiscoverSourceDirs(ctx, rootsOf(t, root), nil)
	results, err = syncAt(t, ctx, dirs, generated, mustConfig(t))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, agentdecl.ActionUnchanged, results[0].Action,
		"the same declaration reached by another spelling must not be compiled twice")
}
