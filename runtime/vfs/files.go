package vfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Files interface {
	ReadFile(ctx context.Context, name string) ([]byte, error)
	WriteFile(ctx context.Context, name string, data []byte) error
	MkdirAll(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	Rename(ctx context.Context, from, to string) error
	Stat(ctx context.Context, name string) (os.FileInfo, error)
	ReadDir(ctx context.Context, name string) ([]os.DirEntry, error)
	WalkDir(ctx context.Context, name string, fn fs.WalkDirFunc) error
	Sub(dir string) (Files, error)
}

var _ Files = (*View)(nil)

func (v *View) Sub(dir string) (Files, error) {
	resolved, err := v.Resolve(dir)
	if err != nil {
		return nil, err
	}
	child, err := newView(resolved)
	if err != nil {
		return nil, err
	}
	child.privileged = v.privileged
	return child, nil
}

var _ Files = (*View)(nil)

func (v *View) ReadFile(_ context.Context, name string) ([]byte, error) {
	abs, err := v.Resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func (v *View) WriteFile(_ context.Context, name string, data []byte) error {
	abs, err := v.Resolve(name)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

func (v *View) MkdirAll(_ context.Context, name string) error {
	abs, err := v.Resolve(name)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o750)
}

func (v *View) Remove(_ context.Context, name string) error {
	abs, err := v.Resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

func (v *View) Rename(_ context.Context, from, to string) error {
	src, err := v.Resolve(from)
	if err != nil {
		return err
	}
	dst, err := v.Resolve(to)
	if err != nil {
		return err
	}
	if src == v.realRoot || dst == v.realRoot {
		return &fs.PathError{Op: "rename", Path: from, Err: errors.New("the root cannot be renamed or replaced")}
	}
	if _, err := os.Lstat(dst); err == nil {
		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(src, dst)
}

func (v *View) Stat(_ context.Context, name string) (os.FileInfo, error) {
	abs, err := v.Resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}

func (v *View) ReadDir(_ context.Context, name string) ([]os.DirEntry, error) {
	abs, err := v.Resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(abs)
}

func (v *View) WalkDir(_ context.Context, name string, fn fs.WalkDirFunc) error {
	abs, err := v.Resolve(name)
	if err != nil {
		return err
	}
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(v.realRoot, p)
		if relErr != nil {
			return relErr
		}
		return fn(filepath.ToSlash(rel), d, err)
	})
}
