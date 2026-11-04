package contenoxcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnit_IsAppBundledPath(t *testing.T) {
	app := t.TempDir()
	resources := filepath.Join(app, "resources")
	runtimeDir := filepath.Join(resources, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(runtimeDir, "contenox-runtime.exe")
	if isAppBundledPath(exe) {
		t.Fatal("bundled without app.asar")
	}
	if err := os.WriteFile(filepath.Join(resources, "app.asar"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !isAppBundledPath(exe) {
		t.Fatal("not bundled next to app.asar")
	}
	if isAppBundledPath(filepath.Join(t.TempDir(), "bin", "contenox-runtime.exe")) {
		t.Fatal("standalone install reported as bundled")
	}
}
