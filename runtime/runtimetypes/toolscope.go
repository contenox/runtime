package runtimetypes

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrInvalidWorkspace = errors.New("workspace must be the absolute path of an existing folder")

func CleanWorkspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", ErrInvalidWorkspace
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", ErrInvalidWorkspace
	}
	return path, nil
}

func WorkspaceCovers(workspace, cwd string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return true
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return false
	}
	ws := filepath.Clean(workspace)
	dir := filepath.Clean(cwd)
	if runtime.GOOS == "windows" {
		ws = strings.ToLower(ws)
		dir = strings.ToLower(dir)
	}
	if ws == dir {
		return true
	}
	rel, err := filepath.Rel(ws, dir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
