package modeldinstall_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeldinstall"
	"golang.org/x/sys/windows"
)

func TestUnit_CommandContextWindowsConfiguresNativeLibraries(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "modeld.cmd")
	cmd := modeldinstall.CommandContext(context.Background(), launcher, "version")
	root := filepath.Dir(launcher)
	backendDir := filepath.Join(root, "lib", "llamacpp")

	if cmd.Path != filepath.Join(root, "modeld.exe") {
		t.Fatalf("Path = %q, want packaged executable", cmd.Path)
	}
	if got := envValue(cmd.Env, "CONTENOX_LLAMA_BACKEND_DIR"); got != backendDir {
		t.Fatalf("CONTENOX_LLAMA_BACKEND_DIR = %q, want %q", got, backendDir)
	}
	path := envValue(cmd.Env, "PATH")
	wantPrefix := backendDir + string(filepath.ListSeparator) + filepath.Join(root, "modeld-libs") + string(filepath.ListSeparator)
	if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(wantPrefix)) {
		t.Fatalf("PATH = %q, want prefix %q", path, wantPrefix)
	}
}

func envValue(environ []string, name string) string {
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func TestUnit_CommandContextWindowsCreatesNoConsoleWindow(t *testing.T) {
	for _, launcher := range []string{
		filepath.Join(t.TempDir(), "modeld.cmd"),
		filepath.Join(t.TempDir(), "modeld.exe"),
	} {
		cmd := modeldinstall.CommandContext(context.Background(), launcher, "version")
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
			t.Fatalf("%s: SysProcAttr = %+v, want a hidden window created without a console", launcher, cmd.SysProcAttr)
		}
	}
}
