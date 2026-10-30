package agentdecl

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/contenox/runtime/runtime/taskengine/llmretry"
	"github.com/pelletier/go-toml/v2"
)

const ConfigFilename = "agents.toml"

//go:embed agents.toml
var shippedConfig []byte

type Config struct {
	Version       int                          `toml:"version"`
	Chain         ChainDefaults                `toml:"chain"`
	Routing       RoutingDefaults              `toml:"routing"`
	ToolsPolicies map[string]map[string]string `toml:"tools_policies"`
	Policy        PolicyDefaults               `toml:"policy"`
	Naming        NamingDefaults               `toml:"naming"`
	Tools         map[string]string            `toml:"tools"`
	Models        map[string]string            `toml:"models"`
	Envelopes     map[string]map[string]any    `toml:"envelopes"`
	Agents        map[string]map[string]any    `toml:"agents"`
}

type ChainDefaults struct {
	TokenLimit     int64                 `toml:"token_limit"`
	MaxTokens      string                `toml:"max_tokens"`
	Think          string                `toml:"think"`
	MainRounds     int                   `toml:"main_rounds"`
	RecoveryRounds int                   `toml:"recovery_rounds"`
	RetryOnFailure int                   `toml:"retry_on_failure"`
	Shift          bool                  `toml:"shift"`
	RetryPolicy    *llmretry.RetryPolicy `toml:"retry_policy"`
}

type RoutingDefaults struct {
	Model          string `toml:"model"`
	Provider       string `toml:"provider"`
	PinModel       bool   `toml:"pin_model"`
	RouterModel    string `toml:"router_model"`
	RouterProvider string `toml:"router_provider"`
	AltModel       string `toml:"alt_model"`
	AltProvider    string `toml:"alt_provider"`
}

type PolicyDefaults struct {
	DefaultAction string                   `toml:"default_action"`
	Compute       ComputeDefaults          `toml:"compute"`
	Attention     AttentionDefaults        `toml:"attention"`
	AlwaysDeny    []StandingRule           `toml:"always_deny"`
	AlwaysAllow   []StandingRule           `toml:"always_allow"`
	Postures      map[string]PostureGrants `toml:"postures"`
}

type ComputeDefaults struct {
	MaxToolCalls     int      `toml:"max_tool_calls"`
	MaxTokens        int      `toml:"max_tokens"`
	OnExhausted      string   `toml:"on_exhausted"`
	MaxTurns         int      `toml:"max_turns"`
	ModelAllowlist   []string `toml:"model_allowlist"`
	BackendAllowlist []string `toml:"backend_allowlist"`
}

type AttentionDefaults struct {
	AllowAgentAnswers   bool `toml:"allow_agent_answers"`
	MaxAgentAnswers     int  `toml:"max_agent_answers"`
	AllowAgentApprovals bool `toml:"allow_agent_approvals"`
	MaxAgentApprovals   int  `toml:"max_agent_approvals"`
}

type StandingRule struct {
	Tools     string `toml:"tools"`
	Tool      string `toml:"tool"`
	WhenKey   string `toml:"when_key"`
	WhenOp    string `toml:"when_op"`
	WhenValue string `toml:"when_value"`
}

type PostureGrants struct {
	LocalFSRead  string `toml:"local_fs_read"`
	LocalFSWrite string `toml:"local_fs_write"`
	LocalShell   string `toml:"local_shell"`
}

type NamingDefaults struct {
	ScopeWithDialect bool `toml:"scope_with_dialect"`
}

func Shipped() (Config, error) {
	var d Config
	if err := toml.Unmarshal(shippedConfig, &d); err != nil {
		return Config{}, fmt.Errorf("agentdecl: parse shipped config: %w", err)
	}
	return d, nil
}

func Load(ctx context.Context, roots ...Root) (Config, error) {
	d, err := Shipped()
	if err != nil {
		return Config{}, err
	}
	for _, root := range roots {
		if root.FS == nil {
			continue
		}
		path := root.Path(ConfigFilename)
		raw, err := root.FS.ReadFile(ctx, ConfigFilename)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("agentdecl: read %s: %w", path, err)
		}
		tools, models, agents := d.Tools, d.Models, d.Agents
		envelopes := d.Envelopes
		d.Envelopes = nil
		if err := toml.Unmarshal(raw, &d); err != nil {
			return Config{}, fmt.Errorf("agentdecl: parse %s: %w", path, err)
		}
		d.Tools = mergeNames(tools, d.Tools)
		d.Models = mergeNames(models, d.Models)
		d.Agents = mergeRawSections(agents, d.Agents)
		d.Envelopes = mergeRawSections(envelopes, d.Envelopes)
	}
	return d, nil
}

func (cfg Config) For(agent string) (Config, error) {
	overlay, ok := cfg.Agents[agent]
	if !ok || len(overlay) == 0 {
		out := cfg
		out.Agents = nil
		return out, nil
	}
	raw, err := toml.Marshal(overlay)
	if err != nil {
		return Config{}, fmt.Errorf("agentdecl: re-encode [agents.%s]: %w", agent, err)
	}

	out := cfg
	toolsPolicies, postures := out.ToolsPolicies, out.Policy.Postures
	deny, allow := out.Policy.AlwaysDeny, out.Policy.AlwaysAllow
	tools, models := out.Tools, out.Models
	envelopes := out.Envelopes
	out.ToolsPolicies, out.Policy.Postures = nil, nil
	out.Policy.AlwaysDeny, out.Policy.AlwaysAllow = nil, nil
	out.Tools, out.Models = nil, nil
	out.Agents, out.Envelopes = nil, nil

	if err := toml.Unmarshal(raw, &out); err != nil {
		return Config{}, fmt.Errorf("agentdecl: parse [agents.%s]: %w", agent, err)
	}

	out.ToolsPolicies = mergeToolsPolicies(toolsPolicies, out.ToolsPolicies)
	out.Policy.Postures = mergePostures(postures, out.Policy.Postures)
	out.Envelopes = mergeRawSections(copyRawSections(envelopes), out.Envelopes)
	out.Policy.AlwaysDeny = concatRules(deny, out.Policy.AlwaysDeny)
	out.Policy.AlwaysAllow = concatRules(allow, out.Policy.AlwaysAllow)
	out.Tools = mergeNames(copyNames(tools), out.Tools)
	out.Models = mergeNames(copyNames(models), out.Models)

	if err := out.Validate(); err != nil {
		return Config{}, fmt.Errorf("agentdecl: [agents.%s]: %w", agent, err)
	}
	return out, nil
}

func (cfg Config) UnknownAgents(known map[string]bool) []string {
	var out []string
	for name := range cfg.Agents {
		if !known[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (cfg Config) Validate() error {
	if cfg.Chain.TokenLimit < 0 {
		return fmt.Errorf("agentdecl: chain.token_limit must be non-negative (0 inherits the session window), got %d", cfg.Chain.TokenLimit)
	}
	if cfg.Chain.MainRounds <= 0 {
		return fmt.Errorf("agentdecl: chain.main_rounds must be positive, got %d", cfg.Chain.MainRounds)
	}
	if cfg.Chain.RecoveryRounds <= 0 {
		return fmt.Errorf("agentdecl: chain.recovery_rounds must be positive, got %d", cfg.Chain.RecoveryRounds)
	}
	for _, p := range []Posture{PostureReadOnly, PostureAskAlways, PostureAutoEdit} {
		if _, ok := cfg.Envelopes[string(p)]; ok {
			continue
		}
		if _, ok := cfg.Policy.Postures[string(p)]; !ok {
			return fmt.Errorf("agentdecl: neither [%s.%s] nor policy.postures.%s is declared", EnvelopeSection, p, p)
		}
	}
	return nil
}

func mergeNames(base, overlay map[string]string) map[string]string {
	if base == nil {
		return overlay
	}
	for k, v := range overlay {
		base[k] = v
	}
	return base
}

func copyRawSections(base map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	return out
}

func copyNames(base map[string]string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	return out
}

func mergeToolsPolicies(base, overlay map[string]map[string]string) map[string]map[string]string {
	if len(overlay) == 0 {
		return base
	}
	out := make(map[string]map[string]string, len(base)+len(overlay))
	for set, knobs := range base {
		copied := make(map[string]string, len(knobs))
		for k, v := range knobs {
			copied[k] = v
		}
		out[set] = copied
	}
	for set, knobs := range overlay {
		if out[set] == nil {
			out[set] = make(map[string]string, len(knobs))
		}
		for k, v := range knobs {
			out[set][k] = v
		}
	}
	return out
}

func mergePostures(base, overlay map[string]PostureGrants) map[string]PostureGrants {
	if len(overlay) == 0 {
		return base
	}
	out := make(map[string]PostureGrants, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func mergeRawSections(base, overlay map[string]map[string]any) map[string]map[string]any {
	if len(overlay) == 0 {
		return base
	}
	out := make(map[string]map[string]any, len(base)+len(overlay))
	for name, section := range base {
		out[name] = section
	}
	for name, section := range overlay {
		if out[name] == nil {
			out[name] = section
			continue
		}
		out[name] = mergeRaw(out[name], section)
	}
	return out
}

func mergeRaw(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		sub, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		prior, ok := out[k].(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		out[k] = mergeRaw(prior, sub)
	}
	return out
}

func concatRules(root, agent []StandingRule) []StandingRule {
	if len(agent) == 0 {
		return root
	}
	out := make([]StandingRule, 0, len(root)+len(agent))
	out = append(out, root...)
	return append(out, agent...)
}
