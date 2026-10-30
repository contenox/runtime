// Command devtool holds the small cross-platform helpers behind the developer make targets, so
// they behave the same from PowerShell, cmd and Unix shells.
//
//	go run ./tools/devtool install         build and install contenox-runtime to the per-user bin dir
//	go run ./tools/devtool uninstall       remove the installed binary
//	go run ./tools/devtool path            print the install path
//	go run ./tools/devtool clean <paths>   remove files and directories (missing paths are fine)
//
// The install path is ~/.local/bin (%USERPROFILE%\.local\bin on Windows); DEV_BIN_DIR overrides
// the directory. Other local tools only rely on the binary name contenox-runtime being on PATH,
// so they never need to know where this repository lives.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const usage = "usage: devtool install|uninstall|path|clean <paths>..."

func main() {
	if len(os.Args) < 2 {
		fail(usage)
	}
	if os.Args[1] == "clean" {
		clean(os.Args[2:])
		return
	}
	if len(os.Args) != 2 {
		fail(usage)
	}
	target, err := installPath()
	if err != nil {
		fail(err.Error())
	}
	switch os.Args[1] {
	case "install":
		install(target)
	case "uninstall":
		uninstall(target)
	case "path":
		fmt.Println(target)
	default:
		fail(usage)
	}
}

func clean(paths []string) {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			fail(fmt.Sprintf("remove %s: %v", p, err))
		}
	}
}

func installPath() (string, error) {
	dir := os.Getenv("DEV_BIN_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		dir = filepath.Join(home, ".local", "bin")
	}
	name := "contenox-runtime"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), nil
}

func install(target string) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fail(fmt.Sprintf("create %s: %v", filepath.Dir(target), err))
	}
	cmd := exec.Command("go", "build", "-o", target, "./cmd/contenox")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fail(fmt.Sprintf("build failed (if contenox-runtime is running, stop it first): %v", err))
	}
	fmt.Printf("Installed %s\n", target)
	if !onPath(filepath.Dir(target)) {
		fmt.Printf("Note: %s is not on PATH. Add it so other tools find contenox-runtime.\n", filepath.Dir(target))
	} else if found, err := exec.LookPath(filepath.Base(target)); err == nil && !samePath(found, target) {
		fmt.Printf("Note: another contenox-runtime comes first on PATH: %s\n", found)
	}
}

func uninstall(target string) {
	err := os.Remove(target)
	switch {
	case err == nil:
		fmt.Printf("Removed %s\n", target)
	case errors.Is(err, fs.ErrNotExist):
		fmt.Printf("Nothing to remove at %s\n", target)
	default:
		fail(fmt.Sprintf("remove %s: %v", target, err))
	}
}

func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if samePath(entry, dir) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "devtool: "+msg)
	os.Exit(1)
}
