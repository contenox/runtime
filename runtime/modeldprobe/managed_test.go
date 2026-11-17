package modeldprobe_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/runtime/modeldprobe"
)

func TestUnit_ManagedDaemonDiscovery(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(root, "installed")
	if err := os.MkdirAll(installed, 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(installed, modeldinstall.LauncherName(runtime.GOOS))
	if err := os.WriteFile(launcher, []byte("launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := modeldinstall.WriteCurrentPointer(root, installed); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTENOX_MODELD_BIN", "")
	pathDir := t.TempDir()
	pathBinary := "modeld"
	if runtime.GOOS == "windows" {
		pathBinary += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pathDir, pathBinary), []byte("PATH candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	detector := modeldprobe.New(root)
	if got := detector.Detect().Binary; got != launcher {
		t.Fatalf("binary=%q want %q", got, launcher)
	}
	t.Setenv("CONTENOX_MODELD_BIN", filepath.Join(root, "missing"))
	detector = modeldprobe.New(root)
	if got := detector.Detect().Binary; got != "" {
		t.Fatalf("invalid explicit override fell through: %q", got)
	}
	args := detector.LauncherArgs("contenox-modeld.exe")
	if len(args) != 3 || args[0] != "owner" || args[1] != "--data-root" || args[2] != root {
		t.Fatalf("args=%v", args)
	}
}
