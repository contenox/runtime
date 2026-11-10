package localtools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grepTreeFixture(t *testing.T) (string, taskengine.ToolsRepo) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package x\nfunc Needle() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("needle in text\nNeedle again\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "c.go"), []byte("// Needle below\nvar Needle = 1\n"), 0o644))
	return root, localtools.NewLocalFSTools(root, nil)
}

func grepTree(t *testing.T, h taskengine.ToolsRepo, args map[string]any) string {
	t.Helper()
	res, dataType, err := h.Exec(context.Background(), time.Now(), args, false, &taskengine.ToolsCall{ToolName: "grep"})
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, dataType)
	return res.(string)
}

func TestUnit_LocalFSTools_GrepDirectorySearchesEveryTextFile(t *testing.T) {
	_, h := grepTreeFixture(t)
	out := grepTree(t, h, map[string]any{"path": ".", "pattern": "Needle"})
	assert.Contains(t, out, "a.go:2: func Needle() {}")
	assert.Contains(t, out, "b.txt:2: Needle again")
	assert.Contains(t, out, "sub/c.go:1: // Needle below")
	assert.Contains(t, out, "sub/c.go:2: var Needle = 1")
	assert.NotContains(t, out, "is a directory")
}

func TestUnit_LocalFSTools_GrepGlobLimitsToMatchingNames(t *testing.T) {
	_, h := grepTreeFixture(t)
	out := grepTree(t, h, map[string]any{"path": "*.go", "pattern": "Needle"})
	assert.Contains(t, out, "a.go:2:")
	assert.Contains(t, out, "sub/c.go:2:")
	assert.NotContains(t, out, "b.txt")

	out = grepTree(t, h, map[string]any{"path": "sub/*.go", "pattern": "Needle"})
	assert.Contains(t, out, "sub/c.go:1:")
	assert.NotContains(t, out, "a.go:")

	out = grepTree(t, h, map[string]any{"path": "**/*.txt", "pattern": "needle"})
	assert.Equal(t, "b.txt:1: needle in text", strings.TrimSpace(out))
}

func TestUnit_LocalFSTools_GrepDirectoryReportsNoMatches(t *testing.T) {
	_, h := grepTreeFixture(t)
	out := grepTree(t, h, map[string]any{"path": ".", "pattern": "nothing-here"})
	assert.Contains(t, out, "no matches in 3 files")
}
