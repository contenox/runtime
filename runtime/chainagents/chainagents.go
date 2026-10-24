package chainagents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskchainservice"
)

const AgentChainFilePrefix = "agent-"

var shippedAgentChains = map[string]bool{
	"chain-contenox": true,
	"chain-acp":      true,
	"chain-acpx":     true,
}

type Result struct {
	Created   []string
	Updated   []string
	Unchanged []string
	Disabled  []string
	Skipped   []string
}

func Discover(ctx context.Context, agents agentregistryservice.Service, roots ...string) (Result, error) {
	var result Result
	if agents == nil {
		return result, fmt.Errorf("chainagents: agent registry is required")
	}

	found, err := scan(ctx, roots)
	if err != nil {
		return result, err
	}

	names := make(map[string]bool, len(found))
	for _, c := range found {
		names[c.name] = true
		action, err := upsert(ctx, agents, c)
		if err != nil {
			return result, fmt.Errorf("chainagents: seed chain agent %q: %w", c.name, err)
		}
		switch action {
		case actionCreated:
			result.Created = append(result.Created, c.name)
		case actionUpdated:
			result.Updated = append(result.Updated, c.name)
		case actionUnchanged:
			result.Unchanged = append(result.Unchanged, c.name)
		case actionSkipped:
			result.Skipped = append(result.Skipped, c.name)
		}
	}

	disabled, err := disableVanished(ctx, agents, names)
	if err != nil {
		return result, err
	}
	result.Disabled = disabled
	return result, nil
}

type candidate struct {
	name    string
	path    string
	chainID string
}

func scan(ctx context.Context, roots []string) ([]candidate, error) {
	var out []candidate
	claimed := map[string]bool{}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("chainagents: resolve chain root %q: %w", root, err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			continue
		}
		files, err := localfileservice.NewPrivileged(abs)
		if err != nil {
			return nil, fmt.Errorf("chainagents: open chain root %q: %w", abs, err)
		}
		chains := taskchainservice.NewLocal(files)
		paths, err := chains.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("chainagents: list chains in %q: %w", abs, err)
		}
		for _, rel := range paths {
			chain, err := chains.Get(ctx, rel)
			if err != nil {
				continue
			}
			if !eligible(filepath.Base(rel), chain.ID) {
				continue
			}
			name := agentName(chain.ID)
			if name == "" || claimed[name] {
				continue
			}
			claimed[name] = true
			out = append(out, candidate{
				name:    name,
				path:    filepath.Join(abs, filepath.FromSlash(rel)),
				chainID: chain.ID,
			})
		}
	}
	return out, nil
}

func eligible(base, chainID string) bool {
	if shippedAgentChains[chainID] {
		return true
	}
	return strings.HasPrefix(strings.ToLower(base), AgentChainFilePrefix)
}

func agentName(chainID string) string { return strings.TrimSpace(chainID) }

type action int

const (
	actionCreated action = iota
	actionUpdated
	actionUnchanged
	actionSkipped
)

func upsert(ctx context.Context, agents agentregistryservice.Service, c candidate) (action, error) {
	existing, err := agents.GetByName(ctx, c.name)
	if errors.Is(err, libdb.ErrNotFound) {
		fresh := &runtimetypes.Agent{
			Name:    c.name,
			Enabled: true,
			Source:  sourceDiscovered(),
		}
		if err := fresh.SetChainConfig(runtimetypes.ChainConfig{Path: c.path, ChainID: c.chainID}); err != nil {
			return actionSkipped, err
		}
		if err := agents.Create(ctx, fresh); err != nil {
			return actionSkipped, err
		}
		return actionCreated, nil
	}
	if err != nil {
		return actionSkipped, err
	}
	if existing.Source == nil || *existing.Source != runtimetypes.AgentSourceDiscovered {
		return actionSkipped, nil
	}
	if existing.Kind == runtimetypes.AgentKindChain {
		if cfg, err := existing.ChainConfig(); err == nil && cfg.Path == c.path && cfg.ChainID == c.chainID {
			return actionUnchanged, nil
		}
	}
	if err := existing.SetChainConfig(runtimetypes.ChainConfig{Path: c.path, ChainID: c.chainID}); err != nil {
		return actionSkipped, err
	}
	if err := agents.Update(ctx, existing); err != nil {
		return actionSkipped, err
	}
	return actionUpdated, nil
}

func disableVanished(ctx context.Context, agents agentregistryservice.Service, found map[string]bool) ([]string, error) {
	all, err := agents.List(ctx, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return nil, fmt.Errorf("chainagents: list declared agents: %w", err)
	}
	var disabled []string
	for _, agent := range all {
		if agent.Source == nil || *agent.Source != runtimetypes.AgentSourceDiscovered {
			continue
		}
		if found[agent.Name] || !agent.Enabled {
			continue
		}
		path := ""
		if cfg, err := agent.ChainConfig(); err == nil {
			path = cfg.Path
		}
		agent.Enabled = false
		if err := agents.Update(ctx, agent); err != nil {
			return nil, fmt.Errorf("chainagents: disable vanished chain agent %q: %w", agent.Name, err)
		}
		slog.Warn("chain agent disabled: its chain file is gone",
			"agent", agent.Name, "chain_path", path,
			"remedy", "restore the file and run 'contenox agent enable "+agent.Name+"', or remove the agent")
		disabled = append(disabled, agent.Name)
	}
	return disabled, nil
}

func sourceDiscovered() *string {
	s := runtimetypes.AgentSourceDiscovered
	return &s
}
