package libsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type isolationPlan struct {
	Exec         string         `json:"exec"`
	Args         []string       `json:"args"`
	Workspace    string         `json:"workspace"`
	Home         string         `json:"home"`
	FS           []planCarveout `json:"fs"`
	Net          []NetCarveout  `json:"net"`
	Loopback     bool           `json:"loopback"`
	Egress       bool           `json:"egress,omitempty"`
	EgressSockFD int            `json:"egress_sock_fd,omitempty"`
	Tap          bool           `json:"tap,omitempty"`
	TapSockFD    int            `json:"tap_sock_fd,omitempty"`
}

type planCarveout struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func buildPlan(spec Spec, execPath string, args []string) (isolationPlan, error) {
	if !filepath.IsAbs(spec.WorkspaceRoot) {
		return isolationPlan{}, fmt.Errorf("%w: WorkspaceRoot %q must be an absolute path", ErrInvalidSpec, spec.WorkspaceRoot)
	}
	if info, err := os.Stat(spec.WorkspaceRoot); err != nil {
		return isolationPlan{}, fmt.Errorf("%w: WorkspaceRoot %q is not usable as the agent's working directory: %v", ErrInvalidSpec, spec.WorkspaceRoot, err)
	} else if !info.IsDir() {
		return isolationPlan{}, fmt.Errorf("%w: WorkspaceRoot %q is not a directory", ErrInvalidSpec, spec.WorkspaceRoot)
	}
	if !filepath.IsAbs(spec.Home) {
		return isolationPlan{}, fmt.Errorf("%w: Home %q must be an absolute path", ErrInvalidSpec, spec.Home)
	}
	if !filepath.IsAbs(execPath) {
		return isolationPlan{}, fmt.Errorf("%w: target %q did not resolve to an absolute path", ErrIsolation, execPath)
	}

	fs := make([]planCarveout, 0, len(spec.FS))
	for _, c := range spec.FS {
		p := resolveTilde(c.Path, spec.Home)
		if !filepath.IsAbs(p) {
			return isolationPlan{}, fmt.Errorf("%w: carve-out path %q did not resolve to an absolute path", ErrIsolation, c.Path)
		}
		fs = append(fs, planCarveout{Path: canonicalizeTopLevel(p), Mode: c.Mode})
	}

	net := append([]NetCarveout(nil), spec.Net...)

	return isolationPlan{
		Exec:      execPath,
		Args:      args,
		Workspace: canonicalizeTopLevel(spec.WorkspaceRoot),
		Home:      filepath.Clean(spec.Home),
		FS:        fs,
		Net:       net,
		Loopback:  spec.NetworkWall,
	}, nil
}

func canonicalizeTopLevel(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

func resolveTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[len("~/"):])
	}
	return p
}
