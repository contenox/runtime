package modeldinstall

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/archiveutil"
)

// extractZip unpacks a .zip archive into dest with the same escape protections as
// extractTarGz. Symlinks in zip are stored as entries whose mode has the symlink
// bit set and whose content is the link target.
func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, zf := range zr.File {
		target, err := archiveutil.SafeJoin(".", zf.Name)
		if err != nil {
			return err
		}
		info := zf.FileInfo()
		switch {
		case info.IsDir():
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			if err := extractZipSymlink(root, zf, target); err != nil {
				return err
			}
		default:
			if err := copyZipFile(root, zf, target, info.Mode()&0o777); err != nil {
				return err
			}
		}
	}
	return archiveutil.ValidateLinks(root)
}

func extractZipSymlink(root *os.Root, zf *zip.File, target string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	link, err := io.ReadAll(io.LimitReader(rc, 4096))
	rc.Close()
	if err != nil {
		return err
	}
	if err := archiveutil.SafeLinkTarget(".", filepath.Dir(target), string(link)); err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return root.Symlink(filepath.FromSlash(string(link)), target)
}

func copyZipFile(root *os.Root, zf *zip.File, target string, mode fs.FileMode) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if mode == 0 {
		mode = 0o644
	}
	return archiveutil.WriteFileFromReader(root, target, rc, mode)
}
