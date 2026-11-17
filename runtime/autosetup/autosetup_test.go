package autosetup_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_ContractIsComplete(t *testing.T) {
	require.NoError(t, autosetup.Contract.Validate())
}

func TestUnit_PreviousOverBudgetModelDoesNotRequireALiveDaemon(t *testing.T) {
	for _, owner := range []string{"absent", "expired"} {
		t.Run(owner, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			t.Setenv("CONTENOX_DATA_ROOT", root)
			binary, err := os.Executable()
			require.NoError(t, err)
			t.Setenv("CONTENOX_MODELD_BIN", binary)
			lease := []byte(`{"instance_id":"expired-test-owner","pid":1,"host":"test","acquired_at":"2000-01-01T00:00:00Z","renewed_at":"2000-01-01T00:00:00Z","ttl":30000000000,"meta":{"endpoint":"127.0.0.1:1","backend":"llama"}}`)
			leasePath := filepath.Join(root, "modeld.lease")
			if owner == "expired" {
				require.NoError(t, os.WriteFile(leasePath, lease, 0600))
			}
			db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "setup.db"), runtimetypes.SchemaSQLite)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			store := runtimetypes.New(db.WithoutTransaction())
			require.NoError(t, clikv.SetString(ctx, store, "default-provider", "modeld"))
			require.NoError(t, clikv.SetString(ctx, store, "default-model", "minicpm5-2b"))
			service, err := autosetup.New(db, libtracker.NewTextActivityTracker(io.Discard))
			require.NoError(t, err)
			model, reused, err := service.Previous(ctx, "", "llama", 1, "f16")
			require.NoError(t, err)
			require.False(t, reused)
			require.Empty(t, model.Name)
			model, reused, err = service.Previous(ctx, "", "llama", 1<<40, "f16")
			require.NoError(t, err)
			require.True(t, reused)
			require.Equal(t, "minicpm5-2b", model.Name)
			require.Equal(t, "minicpm5-2b", clikv.Read(ctx, store, "default-model"))
			if owner == "expired" {
				after, err := os.ReadFile(leasePath)
				require.NoError(t, err)
				require.Equal(t, lease, after)
			} else {
				_, err := os.Stat(leasePath)
				require.True(t, os.IsNotExist(err))
			}
		})
	}
}
