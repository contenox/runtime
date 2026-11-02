package agentdecl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/contenox/runtime/runtime/project"
)

func WorkspacePolicyDir(home, workspaceID string) string {
	return filepath.Join(home, GeneratedDirName, "workspaces", workspaceID)
}

func RenderWorkspaceEnvelopes(ctx context.Context, home, workspaceRoot string) ([]string, error) {
	dir := WorkspacePolicyDir(home, project.LayerKey(workspaceRoot))

	homeRoot, err := LocalRoot(home)
	if err != nil {
		return nil, fmt.Errorf("agentdecl: open home: %w", err)
	}
	projectRoot, err := LocalRoot(filepath.Join(workspaceRoot, project.ContenoxDirName))
	if err != nil {
		return nil, retireWorkspaceDir(dir, nil)
	}
	if _, err := projectRoot.FS.ReadFile(ctx, ConfigFilename); errors.Is(err, fs.ErrNotExist) {
		return nil, retireWorkspaceDir(dir, nil)
	}

	homeCfg, err := Load(ctx, homeRoot)
	if err != nil {
		return nil, err
	}
	workspaceCfg, err := Load(ctx, homeRoot, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("agentdecl: workspace %s: %w", workspaceRoot, err)
	}

	var (
		names    []string
		firstErr error
	)
	for _, name := range workspaceCfg.EnvelopeNames() {
		body, rErr := RenderEnvelopePolicy(workspaceCfg, name, ConfigFilename)
		if rErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("agentdecl: workspace %s: render envelope %q: %w", workspaceRoot, name, rErr)
			}
			continue
		}
		if shipped, hErr := RenderEnvelopePolicy(homeCfg, name, ConfigFilename); hErr == nil && bytes.Equal(shipped, body) {
			continue
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return names, fmt.Errorf("agentdecl: create %s: %w", dir, err)
		}
		target := filepath.Join(dir, EnvelopePolicyFile(name))
		if current, readErr := os.ReadFile(target); readErr != nil || !bytes.Equal(current, body) {
			if err := os.WriteFile(target, body, 0o644); err != nil {
				return names, fmt.Errorf("agentdecl: write %s: %w", target, err)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if firstErr != nil {
		return names, firstErr
	}
	return names, retireWorkspaceDir(dir, names)
}

func retireWorkspaceDir(dir string, keep []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	kept := map[string]bool{}
	for _, name := range keep {
		kept[EnvelopePolicyFile(name)] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || kept[entry.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	if len(keep) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}
