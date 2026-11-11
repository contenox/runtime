package localfileservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_WriteIfMatch_SavesAtomicallyAndReleasesItsLocks(t *testing.T) {
	root := t.TempDir()
	svc, err := New(root)
	require.NoError(t, err)
	ctx := context.Background()

	first, err := svc.Write(ctx, "notes.md", []byte("v1\n"), true)
	require.NoError(t, err)
	before := heldPathLocks()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.WriteIfMatch(ctx, "notes.md", []byte("v2\n"), first.ETag)
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(root, "notes.md"))
	require.NoError(t, err)
	require.Equal(t, "v2\n", string(data), "one save wins; the rest see a newer version and refuse")
	require.Equal(t, before, heldPathLocks(), "a path's lock is dropped once nobody holds it")

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), ".contenox-tmp-"), "no temp file is left behind: %s", e.Name())
	}
}
