package agentdecl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/contenox/runtime/runtime/vfs"
	"github.com/pelletier/go-toml/v2"
)

type SectionKind string

const (
	SectionNamed     SectionKind = "named"
	SectionFlat      SectionKind = "flat"
	SectionSingleton SectionKind = "singleton"
)

var configSections = map[string]SectionKind{
	"envelopes":      SectionNamed,
	"agents":         SectionNamed,
	"tools_policies": SectionNamed,
	"tools":          SectionFlat,
	"models":         SectionFlat,
	"chain":          SectionSingleton,
	"routing":        SectionSingleton,
	"naming":         SectionSingleton,
	"policy":         SectionSingleton,
}

func SectionKindOf(section string) (SectionKind, bool) {
	kind, ok := configSections[section]
	return kind, ok
}

func ConfigSectionNames() []string {
	names := make([]string, 0, len(configSections))
	for name := range configSections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var namedItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func validateItemName(kind SectionKind, name string) error {
	if kind == SectionNamed {
		if !namedItemPattern.MatchString(name) {
			return fmt.Errorf("name %q must be letters, digits, '-' or '_' and start with a letter or digit", name)
		}
		return nil
	}
	if err := validTomlText(name); err != nil {
		return fmt.Errorf("name %q %w", name, err)
	}
	return nil
}

type ConfigLayer struct {
	Path     string
	Raw      string
	Exists   bool
	Revision string
}

func LayerRevision(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func ReadConfigLayer(path string) (ConfigLayer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ConfigLayer{Path: path, Revision: LayerRevision("")}, nil
	}
	if err != nil {
		return ConfigLayer{}, fmt.Errorf("agentdecl: read %s: %w", path, err)
	}
	raw := string(data)
	return ConfigLayer{Path: path, Raw: raw, Exists: true, Revision: LayerRevision(raw)}, nil
}

var layerWriteMu sync.Mutex

func LockLayerWrites() func() {
	layerWriteMu.Lock()
	return layerWriteMu.Unlock
}

func WriteConfigLayer(path, raw string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("agentdecl: create %s: %w", filepath.Dir(path), err)
	}
	if err := vfs.WriteFileAtomic(path, []byte(raw)); err != nil {
		return fmt.Errorf("agentdecl: write %s: %w", path, err)
	}
	return nil
}

func itemPath(section, name string) []string { return []string{section, name} }

func LayerItem(raw, section, name string) (any, bool, error) {
	kind, ok := configSections[section]
	if !ok || kind == SectionSingleton {
		return nil, false, fmt.Errorf("%q is not a section with named items", section)
	}
	parsed, err := parseTomlMap(raw)
	if err != nil {
		return nil, false, err
	}
	value, found := getAtPath(parsed, itemPath(section, name))
	return value, found, nil
}

func LayerSection(raw, section string) (any, bool, error) {
	if _, ok := configSections[section]; !ok {
		return nil, false, fmt.Errorf("unknown section %q", section)
	}
	parsed, err := parseTomlMap(raw)
	if err != nil {
		return nil, false, err
	}
	value, found := getAtPath(parsed, []string{section})
	return value, found, nil
}

func SetLayerItem(raw, section, name string, value any) (string, error) {
	kind, ok := configSections[section]
	if !ok || kind == SectionSingleton {
		return "", fmt.Errorf("%q is not a section with named items", section)
	}
	if err := validateItemName(kind, name); err != nil {
		return "", err
	}
	path := itemPath(section, name)
	if kind == SectionFlat {
		text, isString := value.(string)
		if !isString {
			return "", fmt.Errorf("%s.%s must be a text value", section, name)
		}
		if err := validTomlText(text); err != nil {
			return "", fmt.Errorf("%s.%s %w", section, name, err)
		}
		out, err := setFlatKeyText(raw, []string{section}, name, text)
		if err != nil {
			return "", err
		}
		if err := verifyOnlyPathChanged(raw, out, path, text, false); err != nil {
			return "", err
		}
		return out, nil
	}
	table, isTable := value.(map[string]any)
	if !isTable {
		return "", fmt.Errorf("%s.%s must be an object", section, name)
	}
	rendered, err := renderTomlGroup(path, table)
	if err != nil {
		return "", err
	}
	return applyGroup(raw, path, rendered)
}

func SetLayerSection(raw, section string, value any) (string, error) {
	kind, ok := configSections[section]
	if !ok || kind != SectionSingleton {
		return "", fmt.Errorf("%q is not a single settings section", section)
	}
	table, isTable := value.(map[string]any)
	if !isTable {
		return "", fmt.Errorf("%s must be an object", section)
	}
	rendered, err := renderTomlGroup([]string{section}, table)
	if err != nil {
		return "", err
	}
	return applyGroup(raw, []string{section}, rendered)
}

func applyGroup(raw string, path []string, rendered string) (string, error) {
	out := replaceGroupText(raw, path, rendered)
	wantMap, err := parseTomlMap(rendered)
	if err != nil {
		return "", fmt.Errorf("the generated TOML does not parse: %w", err)
	}
	want, _ := getAtPath(wantMap, path)
	if err := verifyOnlyPathChanged(raw, out, path, want, false); err != nil {
		return "", err
	}
	return out, nil
}

func DeleteLayerItem(raw, section, name string) (string, bool, error) {
	kind, ok := configSections[section]
	if !ok || kind == SectionSingleton {
		return "", false, fmt.Errorf("%q is not a section with named items", section)
	}
	if err := validateItemName(kind, name); err != nil {
		return "", false, err
	}
	path := itemPath(section, name)
	var (
		out     string
		removed bool
	)
	if kind == SectionFlat {
		out, removed = deleteFlatKeyText(raw, []string{section}, name)
	} else {
		out, removed = deleteGroupText(raw, path)
	}
	if !removed {
		return raw, false, nil
	}
	if err := verifyOnlyPathChanged(raw, out, path, nil, true); err != nil {
		return "", false, err
	}
	return out, true, nil
}

func DeleteLayerSection(raw, section string) (string, bool, error) {
	kind, ok := configSections[section]
	if !ok || kind != SectionSingleton {
		return "", false, fmt.Errorf("%q is not a single settings section", section)
	}
	out, removed := deleteGroupText(raw, []string{section})
	if !removed {
		return raw, false, nil
	}
	if err := verifyOnlyPathChanged(raw, out, []string{section}, nil, true); err != nil {
		return "", false, err
	}
	return out, true, nil
}

func ConfigFromTexts(texts ...string) (Config, error) {
	d, err := Shipped()
	if err != nil {
		return Config{}, err
	}
	for i, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		if err := mergeConfigLayer(&d, []byte(text), fmt.Sprintf("layer %d", i+1)); err != nil {
			return Config{}, err
		}
	}
	return d, nil
}

func ConfigProblems(texts ...string) []string {
	cfg, err := ConfigFromTexts(texts...)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	if err := cfg.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	for _, name := range cfg.EnvelopeNames() {
		env, err := cfg.ResolveEnvelope(name)
		if err == nil {
			_, err = TranspileEnvelope(env)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("envelope %q: %v", name, err))
		}
	}
	agentNames := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)
	for _, name := range agentNames {
		if _, err := cfg.For(name); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q: %v", name, err))
		}
	}
	return problems
}

func NewConfigProblems(before, after []string) []string {
	known := map[string]bool{}
	for _, p := range before {
		known[p] = true
	}
	var out []string
	for _, p := range after {
		if !known[p] {
			out = append(out, p)
		}
	}
	return out
}

func EffectiveSection(cfg Config, section string) (any, error) {
	switch section {
	case "envelopes":
		return cfg.Envelopes, nil
	case "agents":
		return cfg.Agents, nil
	case "tools_policies":
		return cfg.ToolsPolicies, nil
	case "tools":
		return cfg.Tools, nil
	case "models":
		return cfg.Models, nil
	}
	var typed any
	switch section {
	case "chain":
		typed = cfg.Chain
	case "routing":
		typed = cfg.Routing
	case "naming":
		typed = cfg.Naming
	case "policy":
		typed = cfg.Policy
	default:
		return nil, fmt.Errorf("unknown section %q", section)
	}
	encoded, err := toml.Marshal(typed)
	if err != nil {
		return nil, err
	}
	return parseTomlMap(string(encoded))
}

func ParseConfigLayer(raw string) (map[string]any, error) {
	return parseTomlMap(raw)
}

func MergePatch(target, patch any) any {
	patchObject, isObject := patch.(map[string]any)
	if !isObject {
		return patch
	}
	targetObject, isTargetObject := target.(map[string]any)
	if !isTargetObject {
		targetObject = map[string]any{}
	}
	out := make(map[string]any, len(targetObject)+len(patchObject))
	for key, value := range targetObject {
		out[key] = deepCopyAny(value)
	}
	for key, value := range patchObject {
		if value == nil {
			delete(out, key)
			continue
		}
		out[key] = MergePatch(out[key], value)
	}
	return out
}

func PatchLayerItem(raw, section, name string, patch any) (string, error) {
	kind, ok := configSections[section]
	if !ok || kind == SectionSingleton {
		return "", fmt.Errorf("%q is not a section with named items", section)
	}
	current, found, err := LayerItem(raw, section, name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s.%s is not in this file", section, name)
	}
	if kind == SectionFlat {
		return SetLayerItem(raw, section, name, patch)
	}
	if _, isObject := patch.(map[string]any); !isObject {
		return "", fmt.Errorf("a patch for %s.%s must be an object", section, name)
	}
	return SetLayerItem(raw, section, name, MergePatch(current, patch))
}

func PatchLayerSection(raw, section string, patch any) (string, error) {
	kind, ok := configSections[section]
	if !ok || kind != SectionSingleton {
		return "", fmt.Errorf("%q is not a single settings section", section)
	}
	if _, isObject := patch.(map[string]any); !isObject {
		return "", fmt.Errorf("a patch for %s must be an object", section)
	}
	current, _, err := LayerSection(raw, section)
	if err != nil {
		return "", err
	}
	return SetLayerSection(raw, section, MergePatch(current, patch))
}
