package archiveutil

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteTar streams regular files from dir in deterministic relative-path order.
// The caller must retain an immutable directory until the operation returns.
func WriteTar(ctx context.Context, dir string, out io.Writer) error {
	if ctx == nil || out == nil || dir == "" {
		return errors.New("tar output requires context, directory and writer")
	}
	writer := tar.NewWriter(contextWriter{ctx: ctx, out: out})
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("tar input is not a regular file: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if err := writer.WriteHeader(&tar.Header{Name: filepath.ToSlash(name), Mode: 0o644, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		return errors.Join(copyErr, file.Close())
	})
	return errors.Join(err, writer.Close())
}

type contextWriter struct {
	ctx context.Context
	out io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(data)
}
