package contenoxcli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_OpenDBAt_SQLiteCommitsDoNotHoldTheWriteLockAcrossAnFsync(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDBAt(ctx, filepath.Join(t.TempDir(), "local.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var mode string
	require.NoError(t, db.WithoutTransaction().QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode)

	const synchronousNormal = 1
	var synchronous int
	require.NoError(t, db.WithoutTransaction().QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous))
	require.Equal(t, synchronousNormal, synchronous)
}
