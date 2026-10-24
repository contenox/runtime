package localtools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/stretchr/testify/require"
)

func writeExecutableBinary(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}
	require.NoError(t, os.WriteFile(p, content, 0755))
	return p
}

func TestUnit_ListDir_AnnotatesExecutableAndLargeFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pkgdir"), 0755))
	writeFile(t, dir, "main.go", "package main\n")
	writeExecutableBinary(t, dir, "contenox", 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": "."})
	require.NoError(t, err)
	lines := strings.Split(res.(string), "\n")

	require.True(t, contains(lines, "pkgdir/"), "directories keep the existing trailing slash: %v", lines)
	require.True(t, contains(lines, "main.go"), "a small non-executable text file gets no suffix at all: %v", lines)
	var contenoxLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "contenox") {
			contenoxLine = l
		}
	}
	require.Equal(t, "contenox* (2.0 MiB)", contenoxLine, "executable + oversized file gets a '*' and a compact size")
}

func TestUnit_ListDir_SmallExecutableGetsNoSizeAnnotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "run.sh")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0755))

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": "."})
	require.NoError(t, err)
	lines := strings.Split(res.(string), "\n")
	require.True(t, contains(lines, "run.sh*"), "executable bit alone still gets the '*' marker: %v", lines)
}

func TestUnit_ListDir_RecursiveAnnotatesExecutableAndLargeFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0755))
	writeExecutableBinary(t, dir, filepath.Join("bin", "contenox"), 2*1024*1024)
	writeFile(t, dir, filepath.Join("bin", "README.md"), "docs\n")

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{
		"path":      "bin",
		"recursive": true,
	})
	require.NoError(t, err)
	listing := res.(string)
	require.Contains(t, listing, "bin/contenox* (2.0 MiB)")
	require.Contains(t, listing, "bin/README.md\n")
}

func TestUnit_ListDir_NonDirectoryErrorDescribesWhatItIs(t *testing.T) {
	dir := t.TempDir()
	writeExecutableBinary(t, dir, "contenox", 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": "contenox"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
	require.Contains(t, err.Error(), "regular file")
	require.Contains(t, err.Error(), "2.0 MiB")
	require.Contains(t, err.Error(), "executable")
	require.Contains(t, err.Error(), "binary")
}

func TestUnit_StatFile_ReportsExecutableAndBinaryFlags(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pkgdir"), 0755))
	writeFile(t, dir, "main.go", "package main\n")
	writeExecutableBinary(t, dir, "contenox", 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)

	type statResult struct {
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		SizeHuman  string `json:"sizeHuman"`
		IsDir      bool   `json:"isDir"`
		Mode       string `json:"mode"`
		Executable bool   `json:"executable"`
		Binary     bool   `json:"binary"`
	}

	statOf := func(path string) statResult {
		res, err := execTool(t, context.Background(), h, "stat_file", map[string]any{"path": path})
		require.NoError(t, err)
		var out statResult
		require.NoError(t, json.Unmarshal([]byte(res.(string)), &out))
		return out
	}

	dirStat := statOf("pkgdir")
	require.True(t, dirStat.IsDir)
	require.False(t, dirStat.Executable, "a directory's traversal bit must not be reported as file-executable")
	require.False(t, dirStat.Binary)

	textStat := statOf("main.go")
	require.False(t, textStat.IsDir)
	require.False(t, textStat.Executable)
	require.False(t, textStat.Binary, "a plain Go source file must not sniff as binary")

	binStat := statOf("contenox")
	require.False(t, binStat.IsDir)
	require.True(t, binStat.Executable, "0755 regular file must report executable:true")
	require.True(t, binStat.Binary, "content full of NUL/high bytes must sniff as binary")
	require.Equal(t, "2.0 MiB", binStat.SizeHuman)
	require.True(t, strings.HasPrefix(binStat.Mode, "-rwx"), "mode string should read like ls -l: %q", binStat.Mode)
}

func TestUnit_StatFile_SniffIsCheapOnLargeFiles(t *testing.T) {
	dir := t.TempDir()
	writeExecutableBinary(t, dir, "contenox", 8*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "stat_file", map[string]any{"path": "contenox"})
	require.NoError(t, err)
	require.Contains(t, res.(string), `"binary":true`)
	require.Contains(t, res.(string), `"executable":true`)
}

func TestUnit_ReadFile_RefusesBinaryWithTeachingError(t *testing.T) {
	dir := t.TempDir()
	writeExecutableBinary(t, dir, "contenox", 4096)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "read_file", map[string]any{"path": "contenox"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to read")
	require.Contains(t, err.Error(), "binary")
	require.Contains(t, err.Error(), "executable")
	require.Contains(t, err.Error(), "stat_file")
}

func TestUnit_ReadFile_ExecutableTextFileIsStillReadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "run.sh")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0755))

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "read_file", map[string]any{"path": "run.sh"})
	require.NoError(t, err)
	require.Contains(t, res.(string), "echo hi")
}

func TestUnit_ReadFile_OversizedBinaryStillBlockedBySizeGuard(t *testing.T) {
	dir := t.TempDir()
	writeExecutableBinary(t, dir, "contenox", 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "read_file", map[string]any{"path": "contenox"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "binary")
}
