package archiveutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/archiveutil"
)

func TestUnit_ReplaceDirectoryPreservesPrevious(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "installed")
	writeDirectory(t, destination, "previous")
	if err := archiveutil.ReplaceDirectory(filepath.Join(root, "missing"), destination); err == nil {
		t.Fatal("missing source succeeded")
	}
	assertDirectory(t, destination, "previous")
	source := filepath.Join(root, "prepared")
	writeDirectory(t, source, "replacement")
	if err := archiveutil.ReplaceDirectory(source, destination); err != nil {
		t.Fatal(err)
	}
	assertDirectory(t, destination, "replacement")
	if _, err := os.Stat(filepath.Join(root, archiveutil.PreviousDirectoryPrefix+"installed")); !os.IsNotExist(err) {
		t.Fatalf("obsolete backup: %v", err)
	}
}

func TestUnit_RecoverDirectoryInterruptedReplacement(t *testing.T) {
	for _, activated := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-activation", true: "after-activation"}[activated], func(t *testing.T) {
			root := t.TempDir()
			backup := filepath.Join(root, archiveutil.PreviousDirectoryPrefix+"installed")
			destination := filepath.Join(root, "installed")
			writeDirectory(t, backup, "previous")
			want := "previous"
			if activated {
				writeDirectory(t, destination, "replacement")
				want = "replacement"
			}
			if err := archiveutil.RecoverDirectories(root); err != nil {
				t.Fatal(err)
			}
			assertDirectory(t, destination, want)
			if err := archiveutil.RecoverDirectories(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func writeDirectory(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertDirectory(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "artifact"))
	if err != nil || string(got) != want {
		t.Fatalf("artifact=%q error=%v want=%q", got, err, want)
	}
}
