package runtimetypes_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_MessageIndices_AgentIDColumn_Reserved(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent_id_migration.db")

	db1, err := libdb.NewSQLiteDBManager(ctx, dbPath, runtimetypes.SchemaSQLite)
	require.NoError(t, err)

	exec1 := db1.WithoutTransaction()

	_, err = exec1.ExecContext(ctx, `INSERT INTO message_indices (id, identity) VALUES ($1, $2)`,
		"idx-no-agent", "identity-1")
	require.NoError(t, err, "agent_id must be nullable: inserting without it must succeed")

	const agentID = "agent-123"
	_, err = exec1.ExecContext(ctx, `INSERT INTO message_indices (id, identity, agent_id) VALUES ($1, $2, $3)`,
		"idx-with-agent", "identity-2", agentID)
	require.NoError(t, err)

	var gotNil sql.NullString
	require.NoError(t, exec1.QueryRowContext(ctx, `SELECT agent_id FROM message_indices WHERE id = $1`, "idx-no-agent").Scan(&gotNil))
	require.False(t, gotNil.Valid, "agent_id must be NULL when not set")

	var gotAgent string
	require.NoError(t, exec1.QueryRowContext(ctx, `SELECT agent_id FROM message_indices WHERE id = $1`, "idx-with-agent").Scan(&gotAgent))
	require.Equal(t, agentID, gotAgent)

	require.NoError(t, db1.Close())

	db2, err := libdb.NewSQLiteDBManager(ctx, dbPath, runtimetypes.SchemaSQLite)
	require.NoError(t, err, "re-applying the schema on an already-migrated db must not error")
	t.Cleanup(func() { _ = db2.Close() })

	exec2 := db2.WithoutTransaction()
	var stillThere string
	require.NoError(t, exec2.QueryRowContext(ctx, `SELECT agent_id FROM message_indices WHERE id = $1`, "idx-with-agent").Scan(&stillThere))
	require.Equal(t, agentID, stillThere, "existing row data must survive the re-applied migration")

	_, err = exec2.ExecContext(ctx, `INSERT INTO message_indices (id, identity) VALUES ($1, $2)`,
		"idx-no-agent-2", "identity-3")
	require.NoError(t, err)
}
