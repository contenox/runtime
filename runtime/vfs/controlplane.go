package vfs

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

var ErrControlPlane = errors.New("path is inside the runtime control plane")

var controlPlaneDenied atomic.Pointer[[]string]

func SetControlPlaneDenied(paths ...string) error {
	resolved := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		r, err := ResolveRoot(p)
		if err != nil {
			return fmt.Errorf("vfs: control-plane path %q: %w", p, err)
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		resolved = append(resolved, r)
	}
	controlPlaneDenied.Store(&resolved)
	return nil
}

func ControlPlaneDenied() []string {
	p := controlPlaneDenied.Load()
	if p == nil {
		return nil
	}
	out := make([]string, len(*p))
	copy(out, *p)
	return out
}

func WithinControlPlane(deniedDirs []string, candidate string) (string, bool) {
	if len(deniedDirs) == 0 {
		return "", false
	}
	resolved, err := ResolveRoot(candidate)
	if err != nil {
		return "", false
	}
	for _, d := range deniedDirs {
		rd, err := ResolveRoot(d)
		if err != nil {
			continue
		}
		if within(rd, resolved) {
			return d, true
		}
	}
	return "", false
}

func IsControlPlanePath(candidate string) (string, bool) {
	return WithinControlPlane(ControlPlaneDenied(), candidate)
}

func deniedResolved(resolvedAbs string) (string, bool) {
	p := controlPlaneDenied.Load()
	if p == nil {
		return "", false
	}
	for _, d := range *p {
		if within(d, resolvedAbs) {
			return d, true
		}
	}
	return "", false
}

func controlPlaneError(requested, deniedDir string) error {
	return fmt.Errorf("%w: %s is inside the runtime's control plane (%s), which is never a workspace — the runtime never lets an agent reach its own governing config, database, or policies", ErrControlPlane, requested, deniedDir)
}
