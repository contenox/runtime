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

	if fi, lerr := os.Lstat(absPath); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %s is a symlink whose target cannot be resolved", ErrEscape, absPath)
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

// OpenFileParent pins the directories of a workspace-relative file without following symlinks.
// The caller owns the returned parent and uses the leaf for subsequent rooted file operations.
func OpenFileParent(root *os.Root, name string, create bool) (*os.Root, string, error) {
	if !filepath.IsLocal(name) || name == "." || name != filepath.Clean(name) {
		return nil, "", fmt.Errorf("invalid workspace file path %q", name)
	}
	parent, err := root.OpenRoot(".")
	if err != nil {
		return nil, "", err
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	for _, part := range parts[:len(parts)-1] {
		st, err := parent.Lstat(part)
		if create && errors.Is(err, os.ErrNotExist) {
			err = parent.Mkdir(part, 0755)
			if err == nil || errors.Is(err, os.ErrExist) {
				st, err = parent.Lstat(part)
			}
		}
		if err != nil || !st.IsDir() {
			parent.Close()
			if err != nil {
				return nil, "", err
			}
			return nil, "", fmt.Errorf("%s is not a directory", part)
		}
		next, err := parent.OpenRoot(part)
		parent.Close()
		if err != nil {
			return nil, "", err
		}
		actual, err := next.Stat(".")
		if err != nil || !os.SameFile(st, actual) {
			next.Close()
			return nil, "", fmt.Errorf("directory %s changed while opening it", part)
		}
		parent = next
	}
	return parent, parts[len(parts)-1], nil
}
