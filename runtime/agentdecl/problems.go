package agentdecl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Problem is one thing a sync or render pass could not do. The boot keeps the
// latest list so the app can show a broken declaration instead of only logging
// it: refused, ignored, or a field the declaration named and this build does not
// carry.
type Problem struct {
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	Field  string `json:"field,omitempty"`
}

var (
	problemMu    sync.Mutex
	lastProblems []Problem
)

// LastProblems reports what the most recent bootstrap could not act on.
func LastProblems() []Problem {
	problemMu.Lock()
	defer problemMu.Unlock()
	out := make([]Problem, len(lastProblems))
	copy(out, lastProblems)
	return out
}

func recordProblems(problems []Problem) {
	problemMu.Lock()
	defer problemMu.Unlock()
	lastProblems = problems
}

func problemsFrom(results []SyncResult) []Problem {
	var out []Problem
	for _, r := range results {
		switch r.Action {
		case ActionRefused, ActionIgnored:
			out = append(out, Problem{Source: r.Source, Name: r.Name, Action: string(r.Action), Reason: r.Reason})
		}
		for _, u := range r.Unmapped {
			out = append(out, Problem{Source: r.Source, Name: r.Name, Action: "not_carried", Field: u.Field, Reason: u.Reason})
		}
	}
	return out
}

// AgentFileStatus is one declaration source as the app sees it: where it is,
// whether it is still the shipped copy, and what the last compile said about it.
type AgentFileStatus struct {
	Path     string    `json:"path"`
	State    string    `json:"state"`
	Problems []Problem `json:"problems,omitempty"`
}

// AgentsDir is the folder the app edits: <home>/agents.
func AgentsDir(home string) string { return filepath.Join(home, NativeSourceDir) }

// AgentFiles lists every declaration under <home>/agents with its state and the
// problems the last compile reported for it. States: shipped (byte-identical to
// the file this build ships), edited (a shipped file the user changed), user
// (everything else).
func AgentFiles(home string) ([]AgentFileStatus, error) {
	shipped := map[string][]byte{}
	files, err := preseedFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if strings.HasPrefix(f.rel, NativeSourceDir+"/") {
			shipped[strings.TrimPrefix(f.rel, NativeSourceDir+"/")] = f.content
		}
	}

	root := AgentsDir(home)
	var out []AgentFileStatus
	problems := LastProblems()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		status := AgentFileStatus{Path: rel, State: "user"}
		if want, ok := shipped[rel]; ok {
			got, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(got, want) {
				status.State = "shipped"
			} else {
				status.State = "edited"
			}
		}
		for _, p := range problems {
			if strings.HasSuffix(filepath.ToSlash(p.Source), rel) {
				status.Problems = append(status.Problems, p)
			}
		}
		out = append(out, status)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TreeSignature changes whenever a declaration or agents.toml changes, so a
// watcher can tell "something moved" without parsing anything.
func TreeSignature(home string) string {
	h := sha256.New()
	record := func(rel string, info fs.FileInfo) {
		fmt.Fprintf(h, "%s|%d|%d\n", rel, info.Size(), info.ModTime().UnixNano())
	}
	root := AgentsDir(home)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, iErr := d.Info()
		if iErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(home, path)
		if relErr != nil {
			return nil
		}
		record(filepath.ToSlash(rel), info)
		return nil
	})
	if info, err := os.Stat(filepath.Join(home, ConfigFilename)); err == nil {
		record(ConfigFilename, info)
	}
	return hex.EncodeToString(h.Sum(nil))
}
