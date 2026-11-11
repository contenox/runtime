package localtools

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/runtime/vfs"
)

func WorkspaceSkip() func(root, rel string, isDir bool) bool {
	names := map[string]bool{".contenox": true}
	for _, name := range DefaultSkipDirNames() {
		names[name] = true
	}
	return func(root, rel string, isDir bool) bool {
		if isDir && names[path.Base(rel)] {
			return true
		}
		if _, denied := vfs.IsControlPlanePath(filepath.Join(root, filepath.FromSlash(rel))); denied {
			return true
		}
		return GitignoreIgnores(root, rel, isDir)
	}
}

func IsAtomicTemp(rel string) bool {
	return strings.HasPrefix(path.Base(rel), AtomicTempPrefix)
}
