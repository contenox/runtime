package contenoxcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspacegrants"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWorkspaceWithDataDir(t *testing.T, dbPath, dataDir string, args ...string) (string, error) {
	t.Helper()
	sub := &cobra.Command{Use: "workspace"}
	sub.AddCommand(&cobra.Command{Use: "add", Args: cobra.ExactArgs(1), RunE: runWorkspaceAdd})
	sub.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: runWorkspaceList})
	root := &cobra.Command{Use: "contenox", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("db", "", "SQLite database path")
	root.PersistentFlags().String("data-dir", "", "Override the .contenox data directory path")
	root.AddCommand(sub)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"--db", dbPath, "--data-dir", dataDir, "workspace"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestUnit_WorkspaceCLI_AddControlPlaneRefused(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, ".contenox")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	dbPath := filepath.Join(t.TempDir(), "workspace-cli.db")

	t.Run("granting the control-plane dir itself is refused", func(t *testing.T) {
		out, err := runWorkspaceWithDataDir(t, dbPath, dataDir, "add", dataDir)
		require.Error(t, err)
		msg := err.Error() + out
		assert.Contains(t, strings.ToLower(msg), "control plane", "the refusal must name the boundary")
		assert.True(t, errors.Is(err, workspacegrants.ErrInvalidGrant), "a control-plane grant is a client input fault, got %v", err)
	})

	t.Run("granting a path UNDER the control plane is refused", func(t *testing.T) {
		sub := filepath.Join(dataDir, "models")
		require.NoError(t, os.MkdirAll(sub, 0o700))
		_, err := runWorkspaceWithDataDir(t, dbPath, dataDir, "add", sub)
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "control plane")
	})

	t.Run("granting the non-control-plane parent still works", func(t *testing.T) {
		out, err := runWorkspaceWithDataDir(t, dbPath, dataDir, "add", parent)
		require.NoError(t, err, "the parent contains the control plane but is a legitimate root; only the control plane itself is refused")
		assert.Contains(t, out, filepath.Clean(parent))
	})
}

func TestUnit_WorkspaceREST_AddControlPlaneRefused(t *testing.T) {
	base := t.TempDir()
	controlPlane := filepath.Join(base, ".contenox")
	require.NoError(t, os.MkdirAll(controlPlane, 0o700))
	dbPath := filepath.Join(t.TempDir(), "reload.db")

	require.NoError(t, vfs.SetControlPlaneDenied(controlPlane))
	t.Cleanup(func() { _ = vfs.SetControlPlaneDenied() })

	factory, err := vfs.NewFactory(base)
	require.NoError(t, err)
	ctx, store, done := storeAt(t, dbPath)
	defer done()

	reloader := newWorkspaceRootReloader(factory, []string{base}, store)
	err = reloader.mutators(nil).Add(ctx, controlPlane, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, workspacegrants.ErrInvalidGrant), "REST control-plane grant is a client fault, got %v", err)
	assert.Contains(t, strings.ToLower(err.Error()), "control plane")

	assert.Empty(t, workspacegrants.ReadGrants(ctx, store), "a refused grant must not be persisted")
}
