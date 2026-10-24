package vfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrEscape = errors.New("path escapes workspace root")

var ErrNotAllowed = errors.New("workspace root is not allowed")

func ResolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("vfs: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("vfs: invalid root: %w", err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("vfs: root resolution error: %w", err)
	}
	return filepath.Clean(abs), nil
}

func Contain(root, candidate string) (string, error) {
	realRoot, err := ResolveRoot(root)
	if err != nil {
		return "", err
	}
	return containWithin(realRoot, root, candidate)
}

func containWithin(realRoot, displayRoot, candidate string) (string, error) {
	return containWithinOpts(realRoot, displayRoot, candidate, false)
}

func containWithinOpts(realRoot, displayRoot, candidate string, privileged bool) (string, error) {
	absPath := candidate
	if !filepath.IsAbs(candidate) {
		absPath = filepath.Join(realRoot, candidate)
	}
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("vfs: invalid path: %w", err)
	}
	real, err := resolveLeaf(absPath)
	if err != nil {
		return "", fmt.Errorf("vfs: path resolution error: %w", err)
	}
	real = filepath.Clean(real)
	if !within(realRoot, real) {
		return "", fmt.Errorf("%w: %s escapes %s", ErrEscape, candidate, displayRoot)
	}
	if !privileged {
		if denied, ok := deniedResolved(real); ok {
			return "", controlPlaneError(candidate, denied)
		}
	}
	return real, nil
}

func Within(root, abs string) bool {
	realRoot, err := ResolveRoot(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	return within(realRoot, filepath.Clean(absPath))
}

func within(realRoot, abs string) bool {
	sep := string(filepath.Separator)
	rel, err := filepath.Rel(realRoot, abs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+sep))
}

func resolveLeaf(absPath string) (string, error) {
	absPath = filepath.Clean(absPath)

	if realPath, err := filepath.EvalSymlinks(absPath); err == nil {
		return filepath.Abs(realPath)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	probe := absPath
	var missing []string

	for {
		realPath, err := filepath.EvalSymlinks(probe)
		if err == nil {
			resolved := realPath
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Abs(resolved)
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}

		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}
