package agentdecl

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"

	"github.com/contenox/runtime/runtime/vfs"
)

//go:embed preseed/reviewer.md
var preseedReviewer string

//go:embed preseed/researcher.md
var preseedResearcher string

//go:embed preseed/README.md
var preseedReadme string

//go:embed preseed/run.md
var preseedRun string

//go:embed preseed/chat.md
var preseedChat string

//go:embed preseed/agents
var preseedTrees embed.FS

var Preseeded = []struct {
	RelPath string
	Source  string
	Content func() string
}{
	{ConfigFilename, "agents.toml", func() string { return string(shippedConfig) }},
	{path.Join(NativeSourceDir, "README.md"), "preseed/README.md", func() string { return preseedReadme }},
	{path.Join(NativeSourceDir, "reviewer.md"), "preseed/reviewer.md", func() string { return preseedReviewer }},
	{path.Join(NativeSourceDir, "researcher.md"), "preseed/researcher.md", func() string { return preseedResearcher }},
	{path.Join(NativeSourceDir, "run.md"), "preseed/run.md", func() string { return preseedRun }},
	{path.Join(NativeSourceDir, "chat.md"), "preseed/chat.md", func() string { return preseedChat }},
}

type PreseedSource struct {
	Rel    string
	Source string
}

func PreseedSources() ([]PreseedSource, error) {
	files, err := preseedFiles()
	if err != nil {
		return nil, err
	}
	sources := make([]PreseedSource, 0, len(files))
	for _, f := range files {
		sources = append(sources, PreseedSource{Rel: f.rel, Source: f.source})
	}
	return sources, nil
}

const PreseedStateFilename = ".preseed-state.json"

type PreseedResult struct {
	Created    []string
	Updated    []string
	Edited     []string
	Unrecorded []string
}

type preseedRecord struct {
	SHA256   string `json:"sha256,omitempty"`
	Operator bool   `json:"operator,omitempty"`
}

type preseedFile struct {
	rel     string
	source  string
	content []byte
}

func Preseed(ctx context.Context, contenoxDir Root) (PreseedResult, error) {
	var result PreseedResult
	if contenoxDir.FS == nil {
		return result, nil
	}
	files, err := preseedFiles()
	if err != nil {
		return result, err
	}
	state := readPreseedState(ctx, contenoxDir.FS)
	next := make(map[string]preseedRecord, len(files))
	for _, f := range files {
		shipped := hashBytes(f.content)
		display := contenoxDir.Path(f.rel)
		onDisk, readErr := contenoxDir.FS.ReadFile(ctx, f.rel)
		record, recorded := state[f.rel]
		hash := hashBytes(onDisk)
		switch {
		case errors.Is(readErr, fs.ErrNotExist):
			if err := writePreseedFile(ctx, contenoxDir, f.rel, f.content); err != nil {
				return result, err
			}
			result.Created = append(result.Created, display)
		case readErr != nil:
			return result, fmt.Errorf("agentdecl: read %s: %w", display, readErr)
		case hash == shipped:
		case blessedPreseedHash(f.rel, hash):
			if err := writePreseedFile(ctx, contenoxDir, f.rel, f.content); err != nil {
				return result, err
			}
			result.Updated = append(result.Updated, display)
		case recorded && record.SHA256 == hash:
			if err := writePreseedFile(ctx, contenoxDir, f.rel, f.content); err != nil {
				return result, err
			}
			result.Updated = append(result.Updated, display)
		case recorded && !record.Operator:
			result.Edited = append(result.Edited, display)
			next[f.rel] = record
			continue
		case recorded:
			next[f.rel] = record
			continue
		default:
			result.Unrecorded = append(result.Unrecorded, display)
			next[f.rel] = preseedRecord{Operator: true}
			continue
		}
		next[f.rel] = preseedRecord{SHA256: shipped}
	}
	if err := writePreseedState(ctx, contenoxDir.FS, state, next); err != nil {
		return result, err
	}
	return result, nil
}

func preseedFiles() ([]preseedFile, error) {
	files := make([]preseedFile, 0, len(Preseeded))
	for _, f := range Preseeded {
		files = append(files, preseedFile{rel: f.RelPath, source: f.Source, content: []byte(f.Content())})
	}
	err := fs.WalkDir(preseedTrees, "preseed/agents", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, relErr := filepath.Rel("preseed/agents", p)
		if relErr != nil {
			return relErr
		}
		data, rErr := preseedTrees.ReadFile(p)
		if rErr != nil {
			return rErr
		}
		files = append(files, preseedFile{
			rel:     path.Join(NativeSourceDir, filepath.ToSlash(rel)),
			source:  path.Join("preseed", "agents", filepath.ToSlash(rel)),
			content: data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

func writePreseedFile(ctx context.Context, contenoxDir Root, rel string, content []byte) error {
	if err := contenoxDir.FS.MkdirAll(ctx, path.Dir(rel)); err != nil {
		return fmt.Errorf("agentdecl: create %s: %w", contenoxDir.Path(path.Dir(rel)), err)
	}
	if err := contenoxDir.FS.WriteFile(ctx, rel, content); err != nil {
		return fmt.Errorf("agentdecl: write %s: %w", contenoxDir.Path(rel), err)
	}
	return nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func blessedPreseedHash(rel, hash string) bool {
	for _, blessed := range blessedPreseedHashes[rel] {
		if blessed == hash {
			return true
		}
	}
	return false
}

func preseedStatePath() string {
	return path.Join(GeneratedDirName, PreseedStateFilename)
}

func readPreseedState(ctx context.Context, fsys vfs.Files) map[string]preseedRecord {
	state := map[string]preseedRecord{}
	raw, err := fsys.ReadFile(ctx, preseedStatePath())
	if err != nil {
		return state
	}
	_ = json.Unmarshal(raw, &state)
	return state
}

func writePreseedState(ctx context.Context, fsys vfs.Files, previous, next map[string]preseedRecord) error {
	before, err := json.MarshalIndent(previous, "", "  ")
	if err != nil {
		return fmt.Errorf("agentdecl: marshal preseed state: %w", err)
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("agentdecl: marshal preseed state: %w", err)
	}
	if string(before) == string(raw) {
		return nil
	}
	if err := fsys.MkdirAll(ctx, GeneratedDirName); err != nil {
		return fmt.Errorf("agentdecl: create %s: %w", GeneratedDirName, err)
	}
	if err := fsys.WriteFile(ctx, preseedStatePath(), raw); err != nil {
		return fmt.Errorf("agentdecl: write preseed state: %w", err)
	}
	return nil
}
