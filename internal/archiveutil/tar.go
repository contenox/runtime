// Package archiveutil provides shared, path-traversal-safe archive
// extraction. It is used both by the modeld install flow (unpacking
// downloaded release bundles) and by node-side model receipt (unpacking a
// pushed OpenVINO IR bundle) — two independent callers writing untrusted
// archive content to disk, where a single reviewed implementation of the
// escape checks matters more than call-site convenience.
package archiveutil

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// unsafePathErr reports an archive entry that would write outside the
// extraction destination. Intentionally unexported and not a sentinel: an
// unsafe archive is a hard failure, never a soft "fall back" case.
func unsafePathErr(name string) error {
	return fmt.Errorf("archiveutil: unsafe archive path %q", name)
}

// SafeJoin resolves an archive entry name (forward-slash separated) against
// dest and guarantees the result stays within dest. It rejects empty paths,
// absolute paths, and `..` traversal.
func SafeJoin(dest, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", unsafePathErr(name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsLocal(clean) {
		return "", unsafePathErr(name)
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", unsafePathErr(name)
	}
	return target, nil
}

// SafeLinkTarget validates a symlink: linkDir is the directory containing the
// link, linkname is the (possibly relative) target as stored in the archive.
// The resolved target must stay within dest, and absolute targets are
// rejected.
func SafeLinkTarget(dest, linkDir, linkname string) error {
	if strings.TrimSpace(linkname) == "" {
		return unsafePathErr(linkname)
	}
	ln := filepath.FromSlash(linkname)
	if filepath.IsAbs(ln) || strings.HasPrefix(ln, string(filepath.Separator)) || filepath.VolumeName(ln) != "" {
		return unsafePathErr(linkname)
	}
	resolved := filepath.Join(linkDir, ln)
	rel, err := filepath.Rel(dest, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return unsafePathErr(linkname)
	}
	return nil
}

// WriteFileFromReader creates target (with parents) and copies r into it with
// the given mode. A duplicate entry cannot replace an existing file or link.
func WriteFileFromReader(root *os.Root, target string, r io.Reader, mode fs.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := root.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil { //nolint:gosec // size bounded by verified archive
		out.Close()
		return err
	}
	return out.Close()
}

// ExtractTar unpacks a tar stream from r into dest, rejecting any entry that
// would escape dest (absolute paths, `..`, escaping sym/hardlinks). Regular
// file modes are preserved. Symlinks and hardlinks are supported but
// validated to stay within dest. r is a raw tar stream — callers wrap it in a
// gzip.Reader first for a .tar.gz archive.
func ExtractTar(r io.Reader, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return ValidateLinks(root)
		}
		if err != nil {
			return err
		}
		target, err := SafeJoin(".", hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := WriteFileFromReader(root, target, tr, fs.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := SafeLinkTarget(".", filepath.Dir(target), hdr.Linkname); err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(filepath.FromSlash(hdr.Linkname), target); err != nil {
				return err
			}
		case tar.TypeLink:
			// Hardlink target is a path within the archive, relative to its root.
			source, err := SafeJoin(".", hdr.Linkname)
			if err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			info, err := root.Lstat(source)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return unsafePathErr(source)
			}
			if err := root.Link(source, target); err != nil {
				return err
			}
		default:
			// Skip devices, fifos, etc. — not expected in any archive this
			// package unpacks (modeld release bundles, pushed model bundles).
		}
	}
}

// ValidateLinks requires each archive symlink to resolve inside root, including
// forward references and chains through other symlinks.
func ValidateLinks(root *os.Root) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			_, err = root.Stat(filepath.FromSlash(name))
		}
		return err
	})
}
