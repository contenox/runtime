package hitlservice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const GeneratedPolicyDir = ".generated"

const envelopeMarker = " Rendered from [envelopes."

const WorkspacePolicySubdir = "workspaces"

const (
	PolicyFromEnvelope = "envelope"
	PolicyFromFile     = "file"
	ScopeWorkspace     = "workspace"
)

type PolicyChoice struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Scope       string `json:"scope,omitempty"`
}

type PolicyLister interface {
	PolicyChoices() []PolicyChoice
}

type WorkspacePolicies interface {
	PolicyChoicesFor(workspaceID string) []PolicyChoice
	ScopedPolicyFile(workspaceID, name string) string
}

func ChoicesFor(src PolicySource, workspaceID string) []PolicyChoice {
	if scoped, ok := src.(WorkspacePolicies); ok && workspaceID != "" {
		return scoped.PolicyChoicesFor(workspaceID)
	}
	return Choices(src)
}

func ResolveForWorkspace(src PolicySource, workspaceID, name string) string {
	if scoped, ok := src.(WorkspacePolicies); ok && workspaceID != "" {
		return scoped.ScopedPolicyFile(workspaceID, name)
	}
	return name
}

func Choices(src PolicySource) []PolicyChoice {
	lister, ok := src.(PolicyLister)
	if !ok {
		return nil
	}
	return lister.PolicyChoices()
}

func ChoiceFiles(choices []PolicyChoice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.File)
	}
	return out
}

func (f *fsPolicySource) PolicyChoices() []PolicyChoice {
	seen := map[string]bool{}
	var out []PolicyChoice
	for _, dir := range f.dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		generated := filepath.Base(filepath.Clean(dir)) == GeneratedPolicyDir
		for _, e := range entries {
			file := e.Name()
			if e.IsDir() || !strings.HasPrefix(file, "hitl-policy-") || !strings.HasSuffix(file, ".json") || seen[file] {
				continue
			}
			seen[file] = true
			data, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				continue
			}
			if choice, ok := describePolicy(file, data, generated); ok {
				out = append(out, choice)
			}
		}
	}
	sortChoices(out)
	return out
}

func sortChoices(out []PolicyChoice) {
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Name == "default") != (out[j].Name == "default") {
			return out[i].Name == "default"
		}
		return out[i].Name < out[j].Name
	})
}

func describePolicy(file string, data []byte, generated bool) (PolicyChoice, bool) {
	choice := PolicyChoice{
		Name: strings.TrimSuffix(strings.TrimPrefix(file, "hitl-policy-"), ".json"),
		File: file,
	}
	var doc struct {
		Note string `json:"//"`
	}
	_ = json.Unmarshal(data, &doc)
	if before, _, found := strings.Cut(doc.Note, envelopeMarker); found {
		choice.Source = PolicyFromEnvelope
		choice.Description = strings.TrimSpace(before)
		return choice, true
	}
	if strings.HasPrefix(strings.TrimSpace(doc.Note), strings.TrimSpace(envelopeMarker)) {
		choice.Source = PolicyFromEnvelope
		return choice, true
	}
	if generated {
		return PolicyChoice{}, false
	}
	choice.Source = PolicyFromFile
	choice.Description = strings.TrimSpace(doc.Note)
	return choice, true
}

func (f *fsPolicySource) PolicyChoicesFor(workspaceID string) []PolicyChoice {
	global := f.PolicyChoices()
	byFile := map[string]int{}
	for i, c := range global {
		byFile[c.File] = i
	}
	for _, dir := range f.dirs {
		if filepath.Base(filepath.Clean(dir)) != GeneratedPolicyDir {
			continue
		}
		wsDir := filepath.Join(dir, WorkspacePolicySubdir, workspaceID)
		entries, err := os.ReadDir(wsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			file := e.Name()
			if e.IsDir() || !strings.HasPrefix(file, "hitl-policy-") || !strings.HasSuffix(file, ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(wsDir, file))
			if err != nil {
				continue
			}
			choice, ok := describePolicy(file, data, true)
			if !ok {
				continue
			}
			choice.Scope = ScopeWorkspace
			if i, exists := byFile[file]; exists {
				global[i] = choice
				continue
			}
			byFile[file] = len(global)
			global = append(global, choice)
		}
	}
	sortChoices(global)
	return global
}

func (f *fsPolicySource) ScopedPolicyFile(workspaceID, name string) string {
	file := policyFileName(name)
	if file == "" || strings.ContainsAny(file, `/\`) {
		return name
	}
	for _, dir := range f.dirs {
		if strings.TrimSpace(dir) == "" || filepath.Base(filepath.Clean(dir)) != GeneratedPolicyDir {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, WorkspacePolicySubdir, workspaceID, file)); err == nil {
			return WorkspacePolicySubdir + "/" + workspaceID + "/" + file
		}
	}
	return name
}
