package localtools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/stretchr/testify/require"
)

// executableName is the fixture name this platform treats as executable:
// Windows keys executability off the extension, Unix off the mode bit.
func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func executableScriptName() string {
	if runtime.GOOS == "windows" {
		return "run.exe"
	}
	return "run.sh"
}

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
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": "."})
	require.NoError(t, err)
	lines := strings.Split(res.(string), "\n")

	require.True(t, contains(lines, "pkgdir/"), "directories keep the existing trailing slash: %v", lines)
	require.True(t, contains(lines, "main.go"), "a small non-executable text file gets no suffix at all: %v", lines)
	var exeLine string
	for _, l := range lines {
		if strings.HasPrefix(l, exe) {
			exeLine = l
		}
	}
	require.Equal(t, exe+"* (2.0 MiB)", exeLine, "executable + oversized file gets a '*' and a compact size")
}

func TestUnit_ListDir_SmallExecutableGetsNoSizeAnnotation(t *testing.T) {
	dir := t.TempDir()
	name := executableScriptName()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0755))

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": "."})
	require.NoError(t, err)
	lines := strings.Split(res.(string), "\n")
	require.True(t, contains(lines, name+"*"), "an executable file still gets the '*' marker: %v", lines)
}

func TestUnit_ListDir_RecursiveAnnotatesExecutableAndLargeFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0755))
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, filepath.Join("bin", exe), 2*1024*1024)
	writeFile(t, dir, filepath.Join("bin", "README.md"), "docs\n")

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "list_dir", map[string]any{
		"path":      "bin",
		"recursive": true,
	})
	require.NoError(t, err)
	listing := res.(string)
	require.Contains(t, listing, "bin/"+exe+"* (2.0 MiB)")
	require.Contains(t, listing, "bin/README.md\n")
}

func TestUnit_ListDir_NonDirectoryErrorDescribesWhatItIs(t *testing.T) {
	dir := t.TempDir()
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "list_dir", map[string]any{"path": exe})
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
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 2*1024*1024)

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

	binStat := statOf(exe)
	require.False(t, binStat.IsDir)
	require.True(t, binStat.Executable, "an executable file must report executable:true")
	require.True(t, binStat.Binary, "content full of NUL/high bytes must sniff as binary")
	require.Equal(t, "2.0 MiB", binStat.SizeHuman)
	require.True(t, strings.HasPrefix(binStat.Mode, "-rw"), "mode string should read like ls -l: %q", binStat.Mode)
}

func TestUnit_StatFile_SniffIsCheapOnLargeFiles(t *testing.T) {
	dir := t.TempDir()
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 8*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	res, err := execTool(t, context.Background(), h, "stat_file", map[string]any{"path": exe})
	require.NoError(t, err)
	require.Contains(t, res.(string), `"binary":true`)
	require.Contains(t, res.(string), `"executable":true`)
}

func TestUnit_ReadFile_RefusesBinaryWithTeachingError(t *testing.T) {
	dir := t.TempDir()
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 4096)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "read_file", map[string]any{"path": exe})
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
	exe := executableName("contenox")
	writeExecutableBinary(t, dir, exe, 2*1024*1024)

	h := localtools.NewLocalFSTools(dir, nil)
	_, err := execTool(t, context.Background(), h, "read_file", map[string]any{"path": exe})
	require.Error(t, err)
	require.Contains(t, err.Error(), "binary")
}
