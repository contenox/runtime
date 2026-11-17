package archiveutil_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/contenox/runtime/internal/archiveutil"
)

func buildTar(t *testing.T, entries ...func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		e(tw)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

func regFile(name string, content []byte) func(*tar.Writer) {
	return func(tw *tar.Writer) {
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := tw.Write(content); err != nil {
			panic(err)
		}
	}
}

func symlink(name, target string) func(*tar.Writer) {
	return func(tw *tar.Writer) {
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
	}
}

func TestUnit_ExtractTar_WritesRegularFiles(t *testing.T) {
	dest := t.TempDir()
	data := buildTar(t, regFile("a/b.txt", []byte("hello")))

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("ExtractTar: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a", "b.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
}

func TestUnit_ExtractTar_RejectsAbsolutePath(t *testing.T) {
	dest := t.TempDir()
	data := buildTar(t, regFile("/etc/evil", []byte("x")))

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err == nil {
		t.Fatal("ExtractTar with absolute path = nil error, want rejection")
	}
}

func TestUnit_ExtractTar_RejectsParentTraversal(t *testing.T) {
	dest := t.TempDir()
	data := buildTar(t, regFile("../escape", []byte("x")))

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err == nil {
		t.Fatal("ExtractTar with ../ path = nil error, want rejection")
	}
}

func TestUnit_ExtractTar_RejectsNestedTraversal(t *testing.T) {
	dest := t.TempDir()
	data := buildTar(t, regFile("sub/../../escape", []byte("x")))

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err == nil {
		t.Fatal("ExtractTar with nested ../ path = nil error, want rejection")
	}
}

func TestUnit_ExtractTar_RejectsSymlinkEscapingDest(t *testing.T) {
	dest := t.TempDir()
	data := buildTar(t, symlink("link", "../../../etc/passwd"))

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err == nil {
		t.Fatal("ExtractTar with escaping symlink = nil error, want rejection")
	}
}

func requireSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	err := os.Symlink("target", filepath.Join(dir, "probe"))
	if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
		t.Skip("this Windows account lacks symlink creation privileges")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnit_ExtractTar_RejectsChainedLink(t *testing.T) {
	requireSymlinks(t)
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	data := buildTar(t, symlink("dir/up", ".."), symlink("dir/up/out", "../outside"), regFile("out/escaped", []byte("escaped")))
	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err == nil {
		t.Fatal("archive crossing extraction root accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file: %v", err)
	}
}

func TestUnit_ExtractTar_AllowsSymlinkWithinDest(t *testing.T) {
	requireSymlinks(t)
	dest := t.TempDir()
	data := buildTar(t,
		regFile("real.txt", []byte("hi")),
		symlink("alias", "real.txt"),
	)

	if err := archiveutil.ExtractTar(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("ExtractTar: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dest, "alias"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "real.txt" {
		t.Fatalf("symlink target = %q, want %q", target, "real.txt")
	}
}

func TestUnit_SafeJoin_RejectsEmptyName(t *testing.T) {
	if _, err := archiveutil.SafeJoin(t.TempDir(), ""); err == nil {
		t.Fatal("SafeJoin(empty) = nil error, want rejection")
	}
}

func TestUnit_SafeJoin_AllowsNestedRelativePath(t *testing.T) {
	dest := t.TempDir()
	got, err := archiveutil.SafeJoin(dest, "a/b/c.xml")
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	want := filepath.Join(dest, "a", "b", "c.xml")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
