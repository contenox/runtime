package modeldconn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func workerConfiguration(root string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(root, "modeld.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read owner execution configuration: %w", err)
	}
	if !json.Valid(raw) {
		return nil, errors.New("owner modeld.json is invalid; repair local inference configuration before starting a worker")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var configuration map[string]any
	if err := decoder.Decode(&configuration); err != nil {
		return nil, fmt.Errorf("owner execution configuration: %w", err)
	}
	if configuration == nil {
		return nil, errors.New("owner execution configuration must be an object")
	}
	return json.Marshal(configuration)
}

func (m *Workers) launchConfigured(ctx context.Context, e *workerEntry, root string) (workerHandle, error) {
	if len(e.configuration) > 0 {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return workerHandle{}, err
		}
		if err := os.WriteFile(filepath.Join(root, "modeld.json"), e.configuration, 0o600); err != nil {
			return workerHandle{}, err
		}
	}
	return m.launch(ctx, e.spec, root)
}
