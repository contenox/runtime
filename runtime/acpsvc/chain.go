package acpsvc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/contenox/runtime/runtime/homedir"

	"github.com/contenox/runtime/runtime/taskengine"
)

const (
	defaultChainFilename = "default-acp-chain.json"
	chainPathEnv         = "CONTENOX_ACP_CHAIN_PATH"
)

type ChainRegistry struct {
	mu           sync.RWMutex
	defaultChain *taskengine.TaskChainDefinition
	source       string
}

func LoadChainRegistry() (*ChainRegistry, error) {
	return LoadChainRegistryFrom(defaultChainFilename, chainPathEnv)
}

func LoadChainRegistryFrom(filename, envVar string) (*ChainRegistry, error) {
	path := os.Getenv(envVar)
	if path == "" {
		dir, err := homedir.Dir()
		if err != nil {
			return nil, fmt.Errorf("acpsvc: cannot determine home directory and %s is not set: %w", envVar, err)
		}
		path = filepath.Join(dir, filename)
	}
	registry, err := LoadChainRegistryPath(path)
	if err != nil {
		return nil, fmt.Errorf("%w (set %s to point at another chain)", err, envVar)
	}
	return registry, nil
}

func LoadChainRegistryPath(path string) (*ChainRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("acpsvc: chain file %q not found; populate it like any other contenox chain: %w", path, err)
	}
	var chain taskengine.TaskChainDefinition
	if err := json.Unmarshal(data, &chain); err != nil {
		return nil, fmt.Errorf("acpsvc: invalid chain JSON at %q: %w", path, err)
	}
	if chain.ID == "" {
		return nil, fmt.Errorf("acpsvc: chain at %q has empty ID", path)
	}
	if len(chain.Tasks) == 0 {
		return nil, fmt.Errorf("acpsvc: chain at %q has no tasks", path)
	}
	return &ChainRegistry{defaultChain: &chain, source: path}, nil
}

func (r *ChainRegistry) Default() *taskengine.TaskChainDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultChain
}

// Reload re-reads the chain from the file it was loaded from, so a session
// started after the declaration watcher recompiled sees the new version without
// a restart. An in-memory chain (shipped-tree fallback) has nothing to reload.
func (r *ChainRegistry) Reload() error {
	r.mu.RLock()
	source := r.source
	r.mu.RUnlock()
	if source == "" || strings.HasPrefix(source, "embedded:") {
		return nil
	}
	next, err := LoadChainRegistryPath(source)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.defaultChain = next.defaultChain
	r.mu.Unlock()
	return nil
}

func (r *ChainRegistry) Source() string { return r.source }

func NewChainRegistryFor(chain *taskengine.TaskChainDefinition, source string) *ChainRegistry {
	return &ChainRegistry{defaultChain: chain, source: source}
}
