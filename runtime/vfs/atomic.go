package vfs

import (
	"os"
	"path/filepath"
)

const AtomicTempPrefix = ".contenox-tmp-"

func WriteFileAtomic(absPath string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(absPath), AtomicTempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(absPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := ReplaceFile(tmpName, absPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
