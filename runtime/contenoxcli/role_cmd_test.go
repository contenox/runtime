package contenoxcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/scriptedtest"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func cloneRoleCmd(source *cobra.Command) *cobra.Command {
	return &cobra.Command{Use: source.Use, Args: source.Args, RunE: source.RunE}
}

func roleTestTree(on, off *cobra.Command) *cobra.Command {
	role := &cobra.Command{Use: "role"}
	role.AddCommand(cloneRoleCmd(on), cloneRoleCmd(off))
	return role
}

func runRoleCmdAt(t *testing.T, tree *cobra.Command, dbPath string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "contenox", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("db", "", "SQLite database path")
	root.AddCommand(tree)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--db", dbPath, "role"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestUnit_RoleCommands_TurnAnOptionalRoleOffAndOnThroughTheDeclaredOperation(t *testing.T) {
	require.True(t, reservedSubcommands["role"])
	dbPath := filepath.Join(t.TempDir(), "local.db")
	tree := roleTestTree(roleOnCmd, roleOffCmd)

	out, err := runRoleCmdAt(t, tree, dbPath, "off", "embedding")
	require.NoError(t, err)
	require.Contains(t, out, "Role embedding: off")

	_, err = runRoleCmdAt(t, tree, dbPath, "on", "embedding")
	require.ErrorContains(t, err, "choose and verify a model for Workspace Search first")

	_, err = runRoleCmdAt(t, tree, dbPath, "off", "primary")
	require.ErrorContains(t, err, "optional roles: next-edit, embedding")

	ctx := context.Background()
	scriptPath := filepath.Join(t.TempDir(), "dialog.json")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`{"model":"scripted-test","embed_dimensions":8,"turns":[{"text":"scripted answer","finish_reason":"stop"}]}`), 0o644))
	db, err := OpenDBAt(ctx, dbPath)
	require.NoError(t, err)
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).CreateBackend(ctx, &runtimetypes.Backend{ID: uuid.NewString(), Name: "scripted", Type: modelrepo.ScriptedTestBackendType, BaseURL: scriptPath}))
	bus := libbus.NewSQLite(db.WithoutTransaction())
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels(), runtimestate.WithTracker(libtracker.NoopTracker{}))
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))
	setup, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
	require.NoError(t, err)
	_, err = setup.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleEmbedding, Provider: modelrepo.ScriptedTestBackendType, Model: scriptedtest.DefaultModelName}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.NoError(t, bus.Close())
	require.NoError(t, db.Close())

	out, err = runRoleCmdAt(t, tree, dbPath, "on", "embedding")
	require.NoError(t, err)
	require.Contains(t, out, "Role embedding: on")
}
