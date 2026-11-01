package localtools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ToolchainRoots() []string {
	if runtime.GOOS == "windows" {
		return windowsToolchainRoots()
	}
	return unixToolchainRoots()
}

func windowsToolchainRoots() []string {
	programFiles := os.Getenv("ProgramFiles")
	localAppData := os.Getenv("LOCALAPPDATA")
	appData := os.Getenv("APPDATA")
	profile := os.Getenv("USERPROFILE")
	programData := os.Getenv("ProgramData")

	var roots []string
	add := func(base string, parts ...string) {
		if strings.TrimSpace(base) == "" {
			return
		}
		roots = append(roots, filepath.Join(append([]string{base}, parts...)...))
	}

	add(programFiles, "nodejs")
	add(programFiles, "Git", "cmd")
	add(programFiles, "Go", "bin")
	add(programFiles, "dotnet")
	add(programFiles, "PowerShell", "7")

	add(localAppData, "Programs", "nodejs")
	add(localAppData, "Programs", "Python")
	add(localAppData, "fnm")
	add(appData, "fnm")
	add(appData, "nvm")
	add(localAppData, "Volta", "bin")
	add(localAppData, "pnpm")
	add(localAppData, "Yarn", "bin")
	add(localAppData, "Microsoft", "WinGet", "Links")

	add(appData, "npm")
	add(profile, "scoop", "shims")
	add(profile, ".cargo", "bin")
	add(profile, "go", "bin")
	add(profile, ".bun", "bin")
	add(profile, ".dotnet", "tools")

	add(programData, "chocolatey", "bin")

	return dedupeRoots(roots)
}

func unixToolchainRoots() []string {
	roots := []string{
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
		"/opt/homebrew/bin",
		"/opt/local/bin",
		"/snap/bin",
		"/nix/var/nix/profiles/default/bin",
		"/usr/local/go/bin",
	}

	home := strings.TrimSpace(os.Getenv("HOME"))
	if home != "" {
		for _, rel := range []string{
			".local/bin",
			"bin",
			"go/bin",
			".cargo/bin",
			".bun/bin",
			".volta/bin",
			".nvm/versions/node",
			".fnm",
			".pyenv/shims",
			".asdf/shims",
			".local/share/pnpm",
			".yarn/bin",
		} {
			roots = append(roots, filepath.Join(home, rel))
		}
	}
	return dedupeRoots(roots)
}

func pathRoots() []string {
	var roots []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		entry = strings.TrimSpace(entry)
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		roots = append(roots, filepath.Clean(entry))
	}
	return dedupeRoots(roots)
}

func CommandLocationAllowed(resolved, allowedDir string, extra []string) bool {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" || !filepath.IsAbs(resolved) {
		return false
	}
	if strings.TrimSpace(allowedDir) != "" && underRoot(resolved, allowedDir) {
		return true
	}
	for _, root := range extra {
		if underRoot(resolved, root) {
			return true
		}
	}
	for _, root := range ToolchainRoots() {
		if underRoot(resolved, root) {
			return true
		}
	}
	for _, root := range pathRoots() {
		if underRoot(resolved, root) {
			return true
		}
	}
	return false
}

func underRoot(path, root string) bool {
	path = comparablePath(path)
	root = comparablePath(root)
	if root == "" || root == "." {
		return false
	}
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(path, strings.TrimSuffix(root, sep)+sep)
}

func comparablePath(p string) string {
	p = filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func dedupeRoots(roots []string) []string {
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		key := comparablePath(root)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, filepath.Clean(root))
	}
	return out
}

func splitToolDirs(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
