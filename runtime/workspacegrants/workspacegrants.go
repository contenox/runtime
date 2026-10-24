package workspacegrants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const ConfigKey = "workspace-roots"

const RootsChangedSubject = "workspace.events.roots_changed"

type RootsChangedEvent struct {
	Roots []string `json:"roots"`
}

type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

var ErrInvalidGrant = errors.New("invalid workspace-root grant")

func ReadGrants(ctx context.Context, store runtimetypes.Store) []string {
	raw := clikv.Read(ctx, store, ConfigKey)
	return splitGrants(raw)
}

func splitGrants(raw string) []string {
	out := []string{}
	for _, p := range filepath.SplitList(raw) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinGrants(roots []string) string {
	return strings.Join(roots, string(filepath.ListSeparator))
}

func normalizeGrant(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("%w: a path is required", ErrInvalidGrant)
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not a valid path: %v", ErrInvalidGrant, path, err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %q does not exist; grant a directory that exists", ErrInvalidGrant, abs)
		}
		return "", fmt.Errorf("%w: cannot stat %q: %v", ErrInvalidGrant, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q is a file, not a directory; a workspace root must be a directory", ErrInvalidGrant, abs)
	}
	if broad, why := isBroadParent(abs); broad {
		return "", fmt.Errorf("%w: %q %s; grant a specific project directory, not a broad parent", ErrInvalidGrant, abs, why)
	}
	return abs, nil
}

func isBroadParent(abs string) (bool, string) {
	sep := string(filepath.Separator)
	if abs == filepath.Dir(abs) {
		return true, "is the filesystem root"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if h, herr := filepath.Abs(home); herr == nil && filepath.Clean(h) == abs {
			return true, "is your home directory"
		}
	}
	if trimmed := strings.Trim(abs, sep); trimmed != "" && !strings.Contains(trimmed, sep) {
		return true, "is a top-level system directory"
	}
	return false, ""
}

func samePath(a, b string) bool {
	ac, err := filepath.Abs(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	bc, err := filepath.Abs(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	return filepath.Clean(ac) == filepath.Clean(bc)
}

func Add(ctx context.Context, store runtimetypes.Store, path string) ([]string, error) {
	normalized, err := normalizeGrant(path)
	if err != nil {
		return nil, err
	}
	grants := ReadGrants(ctx, store)
	for _, g := range grants {
		if samePath(g, normalized) {
			return grants, nil
		}
	}
	grants = append(grants, normalized)
	if err := writeGrants(ctx, store, grants); err != nil {
		return nil, err
	}
	return grants, nil
}

func Remove(ctx context.Context, store runtimetypes.Store, path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: a path is required", ErrInvalidGrant)
	}
	grants := ReadGrants(ctx, store)
	kept := make([]string, 0, len(grants))
	for _, g := range grants {
		if samePath(g, path) {
			continue
		}
		kept = append(kept, g)
	}
	if len(kept) == len(grants) {
		return grants, nil
	}
	if err := writeGrants(ctx, store, kept); err != nil {
		return nil, err
	}
	return kept, nil
}

func writeGrants(ctx context.Context, store runtimetypes.Store, roots []string) error {
	if err := clikv.SetString(ctx, store, ConfigKey, joinGrants(roots)); err != nil {
		return fmt.Errorf("persist workspace-root grants: %w", err)
	}
	return nil
}

func PublishChanged(ctx context.Context, pub Publisher, roots []string) error {
	if pub == nil {
		return nil
	}
	data, err := marshalEvent(roots)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, RootsChangedSubject, data)
}

func marshalEvent(roots []string) ([]byte, error) {
	ev := RootsChangedEvent{Roots: append([]string{}, roots...)}
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace-roots-changed event: %w", err)
	}
	return data, nil
}
