package modelstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/transport"
)

// A named GGUF variant keeps its base, adapter and optional strength together
// so copying the model to another modeld owner preserves its behaviour.
const LoRAFile = "adapter.gguf"
const LoRASettingsFile = "adapter.json"

func BundledLoRA(model string) (*transport.AdapterSpec, error) {
	dir := filepath.Dir(model)
	path := filepath.Join(dir, LoRAFile)
	file, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !file.Mode().IsRegular() || file.Size() == 0 {
		return nil, errors.New("a GGUF LoRA adapter must be a nonempty regular file")
	}
	settings := struct {
		Scale float32 `json:"scale"`
	}{Scale: 1}
	config := filepath.Join(dir, LoRASettingsFile)
	if info, err := os.Lstat(config); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024 {
			return nil, errors.New("invalid LoRA strength configuration")
		}
		data, err := os.ReadFile(config)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("read LoRA strength: %w", err)
		}
		if !(settings.Scale > 0) || math.IsInf(float64(settings.Scale), 0) {
			return nil, errors.New("LoRA strength must be a positive finite number")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &transport.AdapterSpec{Name: LoRAFile, Path: path, Scale: settings.Scale}, nil
}
