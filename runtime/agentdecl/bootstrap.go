package agentdecl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
)

func Bootstrap(ctx context.Context, home string, cfg Config) (PreseedResult, []SyncResult, error) {
	return BootstrapFor(ctx, home, "", cfg)
}

// BootstrapFor is Bootstrap with a project root: its `.contenox/agents/` and
// other tools' agent folders are compiled beside the home's, and a project
// agents.toml layers over the home one. An empty project means home only.
func BootstrapFor(ctx context.Context, home, project string, cfg Config) (PreseedResult, []SyncResult, error) {
	homeRoot, err := LocalRoot(home)
	if err != nil {
		return PreseedResult{}, nil, fmt.Errorf("agentdecl: open home: %w", err)
	}
	preseeded, err := Preseed(ctx, homeRoot)
	if err != nil {
		return PreseedResult{}, nil, err
	}

	generated, err := LocalRoot(filepath.Join(home, GeneratedDirName))
	if err != nil {
		return PreseedResult{}, nil, fmt.Errorf("agentdecl: open generated root: %w", err)
	}
	if err := generated.FS.MkdirAll(ctx, "."); err != nil {
		return PreseedResult{}, nil, fmt.Errorf("agentdecl: create %s: %w", GeneratedDirName, err)
	}

	sourceRoots := []Root{homeRoot}
	var workspaceRoots []Root
	if project != "" {
		if projectRoot, pErr := LocalRoot(project); pErr == nil {
			workspaceRoots = append(workspaceRoots, projectRoot)
			if projectContenox, cErr := projectRoot.Child(".contenox"); cErr == nil {
				sourceRoots = append(sourceRoots, projectContenox)
			}
		}
	}
	dirs := DiscoverSourceDirs(ctx, sourceRoots, workspaceRoots)
	results, err := Sync(ctx, dirs, generated, cfg)
	if err != nil {
		return PreseedResult{}, nil, err
	}
	problems := problemsFrom(results)
	defer func() { recordProblems(problems) }()

	var firstErr error
	for _, name := range cfg.EnvelopeNames() {
		if _, _, rErr := SyncEnvelopePolicy(ctx, cfg, name, generated, ConfigFilename); rErr != nil {
			problems = append(problems, Problem{Source: EnvelopeSection + "." + name, Action: string(ActionRefused), Reason: rErr.Error()})
			if firstErr == nil {
				firstErr = fmt.Errorf("agentdecl: render envelope %q: %w", name, rErr)
			}
		}
	}
	if firstErr != nil {
		return preseeded, results, firstErr
	}
	return preseeded, results, nil
}

func CompileShippedTree(cfg Config) (*taskengine.TaskChainDefinition, error) {
	dir, err := os.MkdirTemp("", "contenox-shipped-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	files, err := preseedFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		abs := filepath.Join(dir, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(abs, f.content, 0o644); err != nil {
			return nil, err
		}
	}
	root, err := LocalRoot(filepath.Join(dir, NativeSourceDir, "acp"))
	if err != nil {
		return nil, err
	}
	tree, err := LoadTree(context.Background(), root, cfg)
	if err != nil {
		return nil, err
	}
	return EmitTree(tree, cfg)
}

// CompileLock serializes compile passes over one home. Every project runtime in
// the app shares the home folder, so two saves must not rewrite .generated at
// the same time; a lock older than the stale window is assumed to be a runtime
// that died and is taken over.
func CompileLock(home string) (release func(), err error) {
	dir := filepath.Join(home, GeneratedDirName)
	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		return nil, mkErr
	}
	path := filepath.Join(dir, ".compile.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			_ = lock.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(openErr) {
			return nil, openErr
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("agentdecl: compile lock %s held longer than 10s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
