package contenoxcli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/messagestore"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func seedScopedSessions(t *testing.T) (string, context.Context, libdbexec.DBManager) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	exec := db.WithoutTransaction()
	require.NoError(t, messagestore.New(exec, "app-workspace").CreateNamedMessageIndex(ctx, "11111111-aaaa-4bbb-8ccc-000000000001", "desktop-user", "acp-f3e5d2ba"))
	require.NoError(t, messagestore.New(exec, "other-workspace").CreateNamedMessageIndex(ctx, "11111111-aaaa-4bbb-8ccc-000000000002", "desktop-user", "shared-name"))
	require.NoError(t, messagestore.New(exec, "app-workspace").CreateNamedMessageIndex(ctx, "11111111-aaaa-4bbb-8ccc-000000000003", "desktop-user", "shared-name"))
	return path, ctx, db
}

func deleteCommand(dbPath string, flags map[string]string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "delete"}
	cmd.Flags().String("db", dbPath, "")
	cmd.Flags().String("data-dir", "", "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().Bool("all", false, "")
	for name, value := range flags {
		_ = cmd.Flags().Set(name, value)
	}
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	return cmd, out
}

func remainingSessionNames(t *testing.T, ctx context.Context, db libdbexec.DBManager) map[string]int {
	t.Helper()
	rows, err := querySessionIndex(ctx, db.WithoutTransaction())
	require.NoError(t, err)
	names := map[string]int{}
	for _, r := range rows {
		names[r.name]++
	}
	return names
}

func TestUnit_SessionDelete_AllReachesSessionsTheAppCreated(t *testing.T) {
	dbPath, ctx, db := seedScopedSessions(t)

	cmd, _ := deleteCommand(dbPath, nil)
	err := runSessionDelete(cmd, []string{"acp-f3e5d2ba"})
	require.Error(t, err, "the CLI's own scope does not contain the app's session")
	require.Equal(t, 1, remainingSessionNames(t, ctx, db)["acp-f3e5d2ba"])

	cmd, out := deleteCommand(dbPath, map[string]string{"all": "true"})
	require.NoError(t, runSessionDelete(cmd, []string{"acp-f3e5d2ba"}))
	require.Equal(t, "Deleted session \"acp-f3e5d2ba\" from workspace app-workspace.\n", out.String())
	require.Equal(t, 0, remainingSessionNames(t, ctx, db)["acp-f3e5d2ba"])

	cmd, _ = deleteCommand(dbPath, map[string]string{"all": "true"})
	err = runSessionDelete(cmd, []string{"acp-f3e5d2ba"})
	require.ErrorContains(t, err, `session "acp-f3e5d2ba" not found in any workspace`)
}

func TestUnit_SessionDelete_AmbiguousNameNeedsAWorkspace(t *testing.T) {
	dbPath, ctx, db := seedScopedSessions(t)

	cmd, _ := deleteCommand(dbPath, map[string]string{"all": "true"})
	err := runSessionDelete(cmd, []string{"shared-name"})
	require.ErrorContains(t, err, "exists in 2 workspaces")
	require.ErrorContains(t, err, "--workspace")
	require.Equal(t, 2, remainingSessionNames(t, ctx, db)["shared-name"])

	cmd, out := deleteCommand(dbPath, map[string]string{"workspace": "other-workspace"})
	require.NoError(t, runSessionDelete(cmd, []string{"shared-name"}))
	require.Contains(t, out.String(), "from workspace other-workspace")
	require.Equal(t, 1, remainingSessionNames(t, ctx, db)["shared-name"])

	cmd, _ = deleteCommand(dbPath, map[string]string{"workspace": "missing-workspace"})
	err = runSessionDelete(cmd, []string{"shared-name"})
	require.ErrorContains(t, err, `not found in workspace "missing-workspace"`)

	cmd, _ = deleteCommand(dbPath, map[string]string{"all": "true"})
	require.NoError(t, runSessionDelete(cmd, []string{"11111111-aaaa-4bbb-8ccc-000000000003"}), "a session id resolves like a name")
	require.Equal(t, 0, remainingSessionNames(t, ctx, db)["shared-name"])
}

func TestUnit_RootHelp_DescribesWhereTheDefaultDatabaseLives(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("db")
	require.NotNil(t, flag)
	require.Contains(t, flag.Usage, ".contenox-harness/local.db file in your home folder")
	require.Contains(t, flag.Usage, `%APPDATA%\ContenoxHarness\local.db`)
	require.NotContains(t, flag.Usage, "~/.contenox-harness/local.db")
}
