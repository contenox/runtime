package modeldinstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const llamaBackendDirEnv = "CONTENOX_LLAMA_BACKEND_DIR"

// CommandContext constructs a worker command with its packaged native libraries.
// Windows batch launchers are resolved to their adjacent executable so arguments
// are passed directly, without interpreting paths or flags through a shell. On
// Windows the command is created without a console window, so a runtime started
// from the desktop does not open one per modeld invocation.
func CommandContext(ctx context.Context, launcher string, args ...string) *exec.Cmd {
	cmd := launcherCommand(ctx, launcher, args...)
	hideConsole(cmd)
	return cmd
}

func launcherCommand(ctx context.Context, launcher string, args ...string) *exec.Cmd {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(launcher), ".cmd") {
		return exec.CommandContext(ctx, launcher, args...)
	}
	root := filepath.Dir(launcher)
	cmd := exec.CommandContext(ctx, filepath.Join(root, "modeld.exe"), args...)
	backendDir := filepath.Join(root, "lib", "llamacpp")
	libs := backendDir + string(os.PathListSeparator) + filepath.Join(root, "modeld-libs")
	env := os.Environ()
	pathFound := false
	backendFound := false
	for i, value := range env {
		name, path, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		switch {
		case strings.EqualFold(name, "PATH"):
			env[i] = name + "=" + libs + string(os.PathListSeparator) + path
			pathFound = true
		case strings.EqualFold(name, llamaBackendDirEnv):
			env[i] = name + "=" + backendDir
			backendFound = true
		}
	}
	if !pathFound {
		env = append(env, "PATH="+libs)
	}
	if !backendFound {
		env = append(env, llamaBackendDirEnv+"="+backendDir)
	}
	cmd.Env = env
	return cmd
}
