package contenoxcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/project"
)

func TestUnit_InitProject_ForcesLocalMarker(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "contenox-init-project-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	parentDir := filepath.Join(tempDir, "parent")
	childDir := filepath.Join(parentDir, "sub1", "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("Failed to create child dir: %v", err)
	}

	parentMarker, err := project.EnsureInProjectRoot(parentDir, "parent-proj")
	if err != nil {
		t.Fatalf("Failed to seed parent marker: %v", err)
	}
	if parentMarker.ID == "" {
		t.Fatal("expected parent marker to carry a non-empty id")
	}

	t.Chdir(childDir)
	resolved, err := ResolveContenoxDir(nil)
	if err != nil {
		t.Fatalf("ResolveContenoxDir: %v", err)
	}
	if want := filepath.Join(parentDir, project.ContenoxDirName); resolved != want {
		t.Fatalf("walk-up should reuse the ancestor marker: want %q, got %q", want, resolved)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	contenoxDir, projectName := resolveProjectInit(cwd, "")

	if want := filepath.Join(childDir, project.ContenoxDirName); contenoxDir != want {
		t.Fatalf("--project must target the local dir: want %q, got %q", want, contenoxDir)
	}
	if projectName != filepath.Base(childDir) {
		t.Fatalf("empty --name should default to the dir basename %q, got %q", filepath.Base(childDir), projectName)
	}

	childMarker, err := project.EnsureInContenoxDir(contenoxDir, projectName)
	if err != nil {
		t.Fatalf("EnsureInContenoxDir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(contenoxDir, project.MarkerFileName)); err != nil {
		t.Fatalf("expected a local marker at %s: %v", contenoxDir, err)
	}
	if childMarker.ID == "" {
		t.Fatal("expected child marker to carry a non-empty id")
	}
	if childMarker.ID == parentMarker.ID {
		t.Fatalf("child marker id %q must differ from parent id %q", childMarker.ID, parentMarker.ID)
	}
	if childMarker.Name != filepath.Base(childDir) {
		t.Fatalf("expected child marker name %q, got %q", filepath.Base(childDir), childMarker.Name)
	}

	if got := ResolveWorkspaceID(contenoxDir); got != childMarker.ID {
		t.Fatalf("ResolveWorkspaceID(child) = %q, want %q", got, childMarker.ID)
	}
}

func TestUnit_ResolveProjectInit_ExplicitNameWins(t *testing.T) {
	cwd := filepath.Join(string(filepath.Separator), "tmp", "some-dir")
	contenoxDir, projectName := resolveProjectInit(cwd, "My Project")
	if want := filepath.Join(cwd, project.ContenoxDirName); contenoxDir != want {
		t.Fatalf("contenoxDir: want %q, got %q", want, contenoxDir)
	}
	if projectName != "My Project" {
		t.Fatalf("explicit name should win: got %q", projectName)
	}
}
