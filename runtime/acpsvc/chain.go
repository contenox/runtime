package acpsvc

import (
	"encoding/json"
	"fmt"
	"github.com/contenox/runtime/runtime/homedir"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/runtime/taskengine"
)

const (
	defaultChainFilename = "default-acp-chain.json"
	chainPathEnv         = "CONTENOX_ACP_CHAIN_PATH"
)

type ChainRegistry struct {
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("acpsvc: chain file %q not found; populate it like any other contenox chain or set %s: %w", path, envVar, err)
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

func (r *ChainRegistry) Default() *taskengine.TaskChainDefinition { return r.defaultChain }

func (r *ChainRegistry) Source() string { return r.source }
