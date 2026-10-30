package agentdecl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
)

const GeneratedDirName = ".generated"

const SyncStateFilename = ".sync-state.json"

const NativeSourceDir = "agents"

var ForeignSourceDirs = []string{
	path.Join(".claude", "agents"),
	path.Join(".agents", "agents"),
}

type SyncResult struct {
	Source   string
	Name     string
	Dialect  Dialect
	Action   WriteAction
	Reason   string
	Unmapped []Unmapped
	MCP      []DeclaredMCPServer
	Remote   []DeclaredRemoteTool
}

type syncRecord struct {
	SourcePath   string `json:"source_path"`
	SourceSHA256 string `json:"source_sha256"`
	ChainFile    string `json:"chain_file"`
	PolicyFile   string `json:"policy_file"`
}

func DiscoverSourceDirs(ctx context.Context, contenoxDirs []Root, workspaceRoots []Root) []SourceDir {
	var found []SourceDir
	seen := map[string]bool{}
	add := func(root Root, rel string, native bool) {
		if root.FS == nil {
			return
		}
		dir, err := root.Child(filepath.ToSlash(rel))
		if err != nil || seen[dir.Key] {
			return
		}
		if info, err := dir.FS.Stat(ctx, "."); err != nil || !info.IsDir() {
			return
		}
		seen[dir.Key] = true
		found = append(found, SourceDir{Root: dir, Native: native})
	}
	for _, root := range contenoxDirs {
		add(root, NativeSourceDir, true)
	}
	for _, root := range workspaceRoots {
		for _, rel := range ForeignSourceDirs {
			add(root, rel, false)
		}
	}
	return found
}

type SyncOption func(*syncOptions)

type syncOptions struct {
	skills []Skill
}

func WithSkills(skills []Skill) SyncOption {
	return func(o *syncOptions) { o.skills = skills }
}

func Sync(ctx context.Context, sourceDirs []SourceDir, generated Root, cfg Config, opts ...SyncOption) ([]SyncResult, error) {
	if generated.FS == nil {
		return nil, fmt.Errorf("agentdecl: generated root %q holds no handle", generated.Key)
	}
	var options syncOptions
	for _, opt := range opts {
		opt(&options)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	sources, err := collectSources(ctx, sourceDirs)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, retireAll(ctx, generated)
	}
	if err := generated.FS.MkdirAll(ctx, "."); err != nil {
		return nil, fmt.Errorf("agentdecl: create %s: %w", generated.Key, err)
	}

	state := readSyncState(ctx, generated.FS)
	next := map[string]syncRecord{}
	results := make([]SyncResult, 0, len(sources))
	declared := map[string]bool{}

	for _, src := range sources {
		path := src.path
		res := SyncResult{Source: path}
		var ir *AgentIR
		var tree *AgentTree
		var err error
		if src.tree {
			dir, cErr := src.root.Child(src.rel)
			if cErr == nil {
				tree, err = LoadTree(ctx, dir, cfg)
			} else {
				err = cErr
			}
			if err == nil {
				ir = tree.MergedIR()
			}
		} else {
			var data []byte
			data, err = src.root.FS.ReadFile(ctx, src.rel)
			if err == nil {
				if src.native {
					ir, err = ParseClaudeCode(path, data, cfg)
				} else {
					ir, err = Parse(path, data, cfg)
				}
			}
		}
		if err != nil {
			res.Action, res.Reason = ActionRefused, err.Error()
			if prev, ok := state[path]; ok {
				next[path] = prev
			}
			results = append(results, res)
			continue
		}
		res.Dialect, res.Unmapped = ir.Source.Dialect, ir.Unmapped
		ir.SystemPrompt = expandSkills(ir.SystemPrompt, options.skills)

		agentCfg := cfg
		if src.native {
			agentCfg.Naming.ScopeWithDialect = false
		}
		res.Name = ir.ScopedName(agentCfg.Naming.ScopeWithDialect)
		res.MCP, res.Remote = ir.DeclaredMCP, ir.DeclaredRemote
		declared[res.Name] = true
		agentCfg, err = agentCfg.For(res.Name)
		if err != nil {
			res.Action, res.Reason = ActionRefused, err.Error()
			if prev, ok := state[path]; ok {
				next[path] = prev
			}
			results = append(results, res)
			continue
		}
		var chain *taskengine.TaskChainDefinition
		if src.tree {
			chain, err = EmitTree(tree, agentCfg)
		} else {
			chain, err = EmitChain(ir, agentCfg)
		}
		if err != nil {
			res.Action, res.Reason = ActionRefused, err.Error()
			if prev, ok := state[path]; ok {
				next[path] = prev
			}
			results = append(results, res)
			continue
		}
		policy, err := EmitPolicy(ir, agentCfg)
		if err != nil {
			res.Action, res.Reason = ActionRefused, err.Error()
			if prev, ok := state[path]; ok {
				next[path] = prev
			}
			results = append(results, res)
			continue
		}
		res.Name = chain.ID
		if ReservedNames[chain.ID] {
			res.Action = ActionRefused
			res.Reason = fmt.Sprintf("%q is a shipped agent; a source file may not take its name", chain.ID)
			results = append(results, res)
			continue
		}

		stem := strings.TrimPrefix(chain.ID, "chain-")
		rec := syncRecord{
			SourcePath:   path,
			SourceSHA256: ir.Source.SHA256,
			ChainFile:    ChainFileFor(chain.ID),
			PolicyFile:   PolicyFileFor(stem),
		}
		if _, taken := cfg.Envelopes[stem]; taken {
			rec.PolicyFile = ""
			res.Unmapped = append(res.Unmapped, Unmapped{
				Field: "posture",
				Reason: fmt.Sprintf("%q is also an envelope in %s, which owns %s; this agent runs under the envelope",
					stem, ConfigFilename, PolicyFileFor(stem)),
			})
		}
		chainPath := generated.Path(rec.ChainFile)

		chainJSON, err := marshalWithSchema(chain, ChainSchemaURL)
		if err != nil {
			return nil, err
		}
		policyJSON, err := marshalWithSchema(policy, PolicySchemaURL)
		if err != nil {
			return nil, err
		}

		policyPath := ""
		policyCurrent := true
		if rec.PolicyFile != "" {
			policyPath = generated.Path(rec.PolicyFile)
			policyCurrent = fileHas(ctx, generated.FS, rec.PolicyFile, policyJSON)
		}
		if _, ok := state[path]; ok && fileHas(ctx, generated.FS, rec.ChainFile, chainJSON) && policyCurrent {
			res.Action = ActionUnchanged
			next[path] = rec
			results = append(results, res)
			continue
		}

		if err := generated.FS.WriteFile(ctx, rec.ChainFile, chainJSON); err != nil {
			return nil, fmt.Errorf("agentdecl: write %s: %w", chainPath, err)
		}
		if policyPath != "" {
			if err := generated.FS.WriteFile(ctx, rec.PolicyFile, policyJSON); err != nil {
				return nil, fmt.Errorf("agentdecl: write %s: %w", policyPath, err)
			}
		}

		if _, had := state[path]; had {
			res.Action = ActionUpdated
		} else {
			res.Action = ActionCreated
		}
		next[path] = rec
		results = append(results, res)
	}

	for src, rec := range state {
		if _, kept := next[src]; kept {
			continue
		}
		_ = generated.FS.Remove(ctx, rec.ChainFile)
		if rec.PolicyFile != "" {
			_ = generated.FS.Remove(ctx, rec.PolicyFile)
		}
	}
	for _, name := range cfg.UnknownAgents(declared) {
		results = append(results, SyncResult{
			Source: fmt.Sprintf("%s [agents.%s]", ConfigFilename, name),
			Name:   name,
			Action: ActionIgnored,
			Reason: "no declaration by that name; the section had no effect",
		})
	}
	if err := writeSyncState(ctx, generated.FS, next); err != nil {
		return results, err
	}
	return results, nil
}

func fileHas(ctx context.Context, fsys vfs.Files, name string, want []byte) bool {
	got, err := fsys.ReadFile(ctx, name)
	return err == nil && bytes.Equal(got, want)
}

func retireAll(ctx context.Context, generated Root) error {
	state := readSyncState(ctx, generated.FS)
	for _, rec := range state {
		_ = generated.FS.Remove(ctx, rec.ChainFile)
		if rec.PolicyFile != "" {
			_ = generated.FS.Remove(ctx, rec.PolicyFile)
		}
	}
	if len(state) == 0 {
		return nil
	}
	return writeSyncState(ctx, generated.FS, map[string]syncRecord{})
}

type sourceFile struct {
	root   Root
	rel    string
	path   string
	native bool
	tree   bool
}

func collectSources(ctx context.Context, dirs []SourceDir) ([]sourceFile, error) {
	var found []sourceFile
	for _, dir := range dirs {
		if dir.FS == nil {
			continue
		}
		err := dir.FS.WalkDir(ctx, ".", func(rel string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if _, err := dir.FS.Stat(ctx, path.Join(rel, AgentFilename)); err == nil {
					found = append(found, sourceFile{root: dir.Root, rel: rel, path: dir.Path(rel), native: dir.Native, tree: true})
					return fs.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(path.Ext(rel), ".md") {
				return nil
			}
			if strings.EqualFold(entry.Name(), "README.md") {
				return nil
			}
			found = append(found, sourceFile{root: dir.Root, rel: rel, path: dir.Path(rel), native: dir.Native})
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("agentdecl: scan %s: %w", dir.Key, err)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	return found, nil
}

func readSyncState(ctx context.Context, fsys vfs.Files) map[string]syncRecord {
	state := map[string]syncRecord{}
	raw, err := fsys.ReadFile(ctx, SyncStateFilename)
	if err != nil {
		return state
	}
	_ = json.Unmarshal(raw, &state)
	return state
}

func writeSyncState(ctx context.Context, fsys vfs.Files, state map[string]syncRecord) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("agentdecl: marshal sync state: %w", err)
	}
	if err := fsys.WriteFile(ctx, SyncStateFilename, raw); err != nil {
		return fmt.Errorf("agentdecl: write sync state: %w", err)
	}
	return nil
}

// PolicyForAgent reports the generated policy of the declaration whose scoped
// name ends in agentName — a declaration read from another product's directory
// carries that product's prefix, so an exact match is not enough.
func PolicyForAgent(ctx context.Context, generatedDir Root, agentName string) (string, bool) {
	if generatedDir.FS == nil || agentName == "" {
		return "", false
	}
	entries, err := generatedDir.FS.ReadDir(ctx, ".")
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "hitl-policy-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(name, "hitl-policy-"), ".json")
		if stem == agentName || strings.HasSuffix(stem, "-"+agentName) {
			return name, true
		}
	}
	return "", false
}

// PolicyForChain reports the generated policy file an agent's compiled chain
// was emitted with, so a caller that knows the chain (a mission naming a
// declared agent, say) can bound itself by that agent's own posture instead of
// a global default. The name is the file under the generated root.
func PolicyForChain(ctx context.Context, generatedDir Root, chainFile string) (string, bool) {
	if generatedDir.FS == nil || chainFile == "" {
		return "", false
	}
	for _, rec := range readSyncState(ctx, generatedDir.FS) {
		if rec.ChainFile == chainFile && rec.PolicyFile != "" {
			return rec.PolicyFile, true
		}
	}
	return "", false
}
