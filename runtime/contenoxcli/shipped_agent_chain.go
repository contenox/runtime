package contenoxcli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/runtime/agentdecl"
)

const (
	chatAgentChainID = "chain-contenox"
	runAgentChainID  = "run"
)

func compiledAgentChainRef(chainID string) string {
	return filepath.ToSlash(filepath.Join(agentdecl.GeneratedDirName, agentdecl.ChainFileFor(chainID)))
}

func resolveCompiledAgentChain(ctx context.Context, contenoxDir, chainID string) (string, error) {
	var dirs []string
	if contenoxDir != "" {
		dirs = append(dirs, contenoxDir)
	}
	if home, err := globalContenoxDir(); err == nil && home != contenoxDir {
		dirs = append(dirs, home)
	}
	find := func() string {
		for _, dir := range dirs {
			path := filepath.Join(dir, filepath.FromSlash(compiledAgentChainRef(chainID)))
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return ""
	}
	if path := find(); path != "" {
		return path, nil
	}
	if len(dirs) > 0 {
		if err := seedAgentDeclarations(ctx, dirs[len(dirs)-1]); err != nil {
			return "", err
		}
		if path := find(); path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("agent %q has no compiled chain under %s in %v; check agents/ and run 'contenox-runtime agent list'", chainID, agentdecl.GeneratedDirName, dirs)
}

var retiredChainFiles = map[string]string{
	"default-chain.json":      "agents/contenox/",
	"default-run-chain.json":  "agents/run.md",
	"default-acp-chain.json":  "agents/acp/",
	"headless-acp-chain.json": "agents/acpx.md",
}

func retiredChainFile(name string) bool {
	_, retired := retiredChainFiles[filepath.Base(strings.TrimSpace(name))]
	return retired && !filepath.IsAbs(strings.TrimSpace(name))
}

func warnRetiredChainFiles(contenoxDir string) {
	for name, successor := range retiredChainFiles {
		path := filepath.Join(contenoxDir, name)
		if _, err := os.Stat(path); err == nil {
			slog.Warn("contenox-runtime: this chain file is no longer read; the agent compiles from its declaration, so edit that instead",
				"ignored", path, "declaration", filepath.Join(contenoxDir, filepath.FromSlash(successor)))
		}
	}
}
