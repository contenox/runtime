package agentdecl

import (
	"fmt"
	"path"
	"path/filepath"

	"github.com/contenox/runtime/runtime/vfs"
)

type Root struct {
	Key   string
	FS    vfs.Files
	Local bool
}

func LocalRoot(dir string) (Root, error) {
	handle, err := vfs.OpenPrivilegedView(dir)
	if err != nil {
		return Root{}, err
	}
	return Root{Key: handle.Root(), FS: handle, Local: true}, nil
}

type SourceDir struct {
	Root
	Native bool
}

func (r Root) Child(rel string) (Root, error) {
	if r.FS == nil {
		return Root{}, fmt.Errorf("agentdecl: root %q holds no handle", r.Key)
	}
	handle, err := r.FS.Sub(rel)
	if err != nil {
		return Root{}, err
	}
	return Root{Key: r.Path(rel), FS: handle, Local: r.Local}, nil
}

func (r Root) Path(rel string) string {
	if rel == "" || rel == "." {
		return r.Key
	}
	if r.Local {
		return filepath.Join(r.Key, filepath.FromSlash(rel))
	}
	return path.Join(r.Key, rel)
}

func (r Root) Base() string {
	if r.Local {
		return filepath.Base(r.Key)
	}
	return path.Base(r.Key)
}
