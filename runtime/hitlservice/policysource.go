package hitlservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type PolicySource interface {
	ReadPolicy(ctx context.Context, tenantID, name string) ([]byte, error)
}

type fsPolicySource struct{ dirs []string }

func NewFSPolicySource(dirs ...string) PolicySource {
	return &fsPolicySource{dirs: dirs}
}

func (f *fsPolicySource) ReadPolicy(_ context.Context, _, name string) ([]byte, error) {
	var lastErr error = os.ErrNotExist
	for _, dir := range f.dirs {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("hitl policy %q not found: %w", name, lastErr)
}
