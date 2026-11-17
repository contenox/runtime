package archiveutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreviousDirectoryPrefix reserves sibling directories for interrupted replacements.
const PreviousDirectoryPrefix = ".previous-"

// RecoverDirectory restores an interrupted replacement or removes its obsolete backup.
// The caller must exclude other writers and readers for dst during recovery.
func RecoverDirectory(dst string) error {
	previous := filepath.Join(filepath.Dir(dst), PreviousDirectoryPrefix+filepath.Base(dst))
	if _, err := os.Lstat(previous); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return os.Rename(previous, dst)
	} else if err != nil {
		return err
	}
	return os.RemoveAll(previous)
}

// RecoverDirectories recovers interrupted replacements before a directory store opens.
// No reader or writer may use the store until this operation succeeds.
func RecoverDirectories(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), PreviousDirectoryPrefix) {
			continue
		}
		name := strings.TrimPrefix(entry.Name(), PreviousDirectoryPrefix)
		if name == "" || name == "." || name == ".." {
			return fmt.Errorf("invalid replacement backup %q", entry.Name())
		}
		if err := RecoverDirectory(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceDirectory activates src while retaining dst until activation succeeds.
// The caller must exclude readers and writers for dst. RecoverDirectory restores
// an interrupted replacement before the destination is used after a restart.
func ReplaceDirectory(src, dst string) error {
	if err := RecoverDirectory(dst); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return os.Rename(src, dst)
	} else if err != nil {
		return err
	}
	previous := filepath.Join(filepath.Dir(dst), PreviousDirectoryPrefix+filepath.Base(dst))
	if err := os.Rename(dst, previous); err != nil {
		return fmt.Errorf("preserve previous installation: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		if restoreErr := os.Rename(previous, dst); restoreErr != nil {
			return fmt.Errorf("replacement failed; previous installation retained at %s: %w", previous, errors.Join(err, restoreErr))
		}
		return fmt.Errorf("activate replacement: %w", err)
	}
	_ = os.RemoveAll(previous)
	return nil
}
