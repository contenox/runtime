package vfs_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/vfs"
	"github.com/stretchr/testify/require"
)

func TestIntegration_WriteFileAtomic_WaitsOutAReaderHoldingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.toml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))
	reader, err := os.Open(path)
	require.NoError(t, err)
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = reader.Close()
		close(released)
	}()

	require.NoError(t, vfs.WriteFileAtomic(path, []byte("new\n")), "a reader that lets go within the retry window does not fail the save")
	<-released
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), vfs.AtomicTempPrefix+"*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}
