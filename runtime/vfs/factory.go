package vfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
)

type Factory struct {
	current atomic.Pointer[rootSet]
}

type rootSet struct {
	roots   []string
	display map[string]string
}

func buildRootSet(roots ...string) (*rootSet, error) {
	rs := &rootSet{display: map[string]string{}}
	seen := map[string]struct{}{}
	for _, r := range roots {
		if r == "" {
			continue
		}
		resolved, err := ResolveRoot(r)
		if err != nil {
			return nil, fmt.Errorf("vfs: workspace root %q: %w", r, err)
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		rs.roots = append(rs.roots, resolved)
		if _, ok := rs.display[resolved]; !ok {
			rs.display[resolved] = filepath.Clean(r)
		}
	}
	if len(rs.roots) == 0 {
		return nil, fmt.Errorf("vfs: at least one workspace root is required")
	}
	return rs, nil
}

func (rs *rootSet) defaultRoot() string {
	if len(rs.roots) == 0 {
		return ""
	}
	return rs.roots[0]
}

func (rs *rootSet) describe() string {
	return strings.Join(rs.roots, ", ")
}

func NewFactory(roots ...string) (*Factory, error) {
	rs, err := buildRootSet(roots...)
	if err != nil {
		return nil, err
	}
	f := &Factory{}
	f.current.Store(rs)
	return f, nil
}

func (f *Factory) SetRoots(roots []string) error {
	rs, err := buildRootSet(roots...)
	if err != nil {
		return err
	}
	f.current.Store(rs)
	return nil
}

func (f *Factory) load() *rootSet {
	if rs := f.current.Load(); rs != nil {
		return rs
	}
	return &rootSet{display: map[string]string{}}
}

func (f *Factory) Roots() []string {
	rs := f.load()
	out := make([]string, len(rs.roots))
	copy(out, rs.roots)
	return out
}

func (f *Factory) Default() string {
	return f.load().defaultRoot()
}

func (f *Factory) DescribeRoots() string {
	return f.load().describe()
}

func (f *Factory) Resolve(root string) (string, error) {
	rs := f.load()
	if root == "" || root == "/" {
		return rs.defaultRoot(), nil
	}
	resolved, err := ResolveRoot(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotAllowed, err)
	}
	if denied, ok := deniedResolved(resolved); ok {
		return "", controlPlaneError(root, denied)
	}
	for _, r := range rs.roots {
		if within(r, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not under any configured workspace root; roots: %s", ErrNotAllowed, root, rs.describe())
}

func (f *Factory) Allows(root string) (string, bool) {
	resolved, err := f.Resolve(root)
	if err != nil {
		return "", false
	}
	return resolved, true
}

var ErrCwdNotPermitted = errors.New("session cwd is not permitted")

type cwdError struct{ msg string }

func (e *cwdError) Error() string { return e.msg }
func (e *cwdError) Is(target error) bool {
	return target == ErrCwdNotPermitted
}

func cwdRefusal(format string, args ...any) error {
	return &cwdError{msg: fmt.Sprintf(format, args...)}
}

func ResolveSessionCwd(f *Factory, cwd, fallback string) (string, error) {
	if cwd != "" && !filepath.IsAbs(cwd) {
		return "", cwdRefusal("cwd must be an absolute path, got %q", cwd)
	}
	if f == nil {
		if cwd == "" {
			return fallback, nil
		}
		if denied, ok := IsControlPlanePath(cwd); ok {
			return "", cwdRefusal("workspace directory %q is inside the runtime's control plane (%s), which is never a session workspace", cwd, denied)
		}
		return cwd, nil
	}
	resolved, err := f.Resolve(cwd)
	if err != nil {
		if errors.Is(err, ErrControlPlane) {
			return "", cwdRefusal("workspace directory %q is inside the runtime's control plane, which is never a session workspace — the runtime never runs a session where it could reach its own config, database, or policies", cwd)
		}
		return "", cwdRefusal("workspace directory %q is not under any configured workspace root; roots: %s", cwd, f.DescribeRoots())
	}
	return resolved, nil
}

func (f *Factory) Open(root string) (*View, error) {
	resolved, err := f.Resolve(root)
	if err != nil {
		return nil, err
	}
	return newView(resolved)
}

type View struct {
	root       string
	realRoot   string
	privileged bool
}

func OpenView(root string) (*View, error) {
	resolved, err := ResolveRoot(root)
	if err != nil {
		return nil, err
	}
	return newView(resolved)
}

func OpenPrivilegedView(root string) (*View, error) {
	v, err := OpenView(root)
	if err != nil {
		return nil, err
	}
	v.privileged = true
	return v, nil
}

func newView(resolved string) (*View, error) {
	realRoot, err := ResolveRoot(resolved)
	if err != nil {
		return nil, err
	}
	return &View{root: resolved, realRoot: realRoot}, nil
}

func (v *View) Root() string { return v.root }

func (v *View) Resolve(candidate string) (string, error) {
	return containWithinOpts(v.realRoot, v.root, candidate, v.privileged)
}

func (v *View) Contains(abs string) bool {
	absPath, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	return within(v.realRoot, filepath.Clean(absPath))
}
