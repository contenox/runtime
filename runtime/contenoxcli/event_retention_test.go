package contenoxcli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_SeedEventRetention_SetsTheShippedValueOnceAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDBAt(ctx, filepath.Join(t.TempDir(), "local.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	seeded, err := seedEventRetention(ctx, store)
	require.NoError(t, err)
	require.True(t, seeded)
	require.Equal(t, clikv.ShippedEventRetentionDays, clikv.Read(ctx, store, clikv.EventRetentionDaysKey))

	require.NoError(t, clikv.SetString(ctx, store, clikv.EventRetentionDaysKey, "off"))
	seeded, err = seedEventRetention(ctx, store)
	require.NoError(t, err)
	require.False(t, seeded, "an operator's choice, even off, is kept")
	require.Equal(t, "off", clikv.Read(ctx, store, clikv.EventRetentionDaysKey))
}
