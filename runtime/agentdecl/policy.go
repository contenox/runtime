package agentdecl

import (
	"errors"
	"fmt"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missiontools"
)

const PolicySchemaURL = "https://contenox.com/schema/hitl-policy-v1.schema.json"

type ErrUnsafePosture struct {
	Name    string
	Dialect Dialect
}

func (e *ErrUnsafePosture) Error() string {
	return fmt.Sprintf("agentdecl: %s agent %q asks for permissionMode: bypassPermissions — to skip every approval. "+
		"contenox will not run an agent that way. Use acceptEdits and grant what it actually needs "+
		"under [policy.postures] or [[policy.always_allow]] in agents.toml, where the grant is written down",
		e.Dialect, e.Name)
}

func EmitPolicy(ir *AgentIR, cfg Config) (*hitlservice.Policy, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if ir.Posture == PostureUnsafe {
		return nil, &ErrUnsafePosture{Name: ir.Name, Dialect: ir.Source.Dialect}
	}

	if _, err := parseAction(cfg.Policy.DefaultAction); err != nil {
		return nil, fmt.Errorf("agentdecl: policy.default_action: %w", err)
	}
	env, err := cfg.PostureEnvelope(ir.Posture)
	if err != nil {
		return nil, err
	}
	env.AlwaysDeny = concatStandingRules(cfg.Policy.AlwaysDeny, env.AlwaysDeny)
	env.AlwaysAllow = concatStandingRules(cfg.Policy.AlwaysAllow, env.AlwaysAllow)
	if env.DefaultAction.Grant == "" {
		env.DefaultAction.Grant = cfg.Policy.DefaultAction
	}

	out, err := env.transpile(transpileOptions{extraRules: missionToolRules(ir)})
	if err != nil {
		return nil, err
	}
	policy := out.Policy
	if len(env.Compute) == 0 {
		policy.Compute = computeBounds(ir, cfg)
	}
	if len(env.Attention) == 0 && env.Axes[AxisMissionsAnswer].Grant == "" {
		policy.Attention = attentionBounds(ir, cfg)
	}
	policy.Version = hitlservice.PolicySchemaVersion
	return policy, nil
}

func (cfg Config) PostureEnvelope(p Posture) (Envelope, error) {
	env, err := cfg.ResolveEnvelope(string(p))
	switch {
	case err == nil:
		return env, nil
	case !errors.Is(err, ErrNoEnvelope):
		return Envelope{}, err
	}
	grants, ok := cfg.Policy.Postures[string(p)]
	if !ok {
		return Envelope{}, fmt.Errorf("agentdecl: no posture grants configured for %q", p)
	}
	out := Envelope{Name: string(p), Axes: map[string]AxisGrant{}}
	for axis, action := range map[string]string{
		AxisFilesRead:  grants.LocalFSRead,
		AxisFilesWrite: grants.LocalFSWrite,
		AxisShell:      grants.LocalShell,
	} {
		if action == "" {
			continue
		}
		if _, err := parseAction(action); err != nil {
			return Envelope{}, fmt.Errorf("agentdecl: posture %q: %w", p, err)
		}
		out.Axes[axis] = AxisGrant{Grant: action}
	}
	return out, nil
}

func missionToolRules(ir *AgentIR) []hitlservice.Rule {
	if !ir.RunsAsSubagent() {
		return nil
	}
	names := []string{
		missiontools.ToolNameReport,
		missiontools.ToolNamePlan,
		missiontools.ToolNameAskAttention,
		missiontools.ToolNameFinish,
	}
	out := make([]hitlservice.Rule, 0, len(names))
	for _, name := range names {
		out = append(out, hitlservice.Rule{
			Tools:  missiontools.ToolsProviderName,
			Tool:   name,
			Action: hitlservice.ActionAllow,
		})
	}
	return out
}

func (ir *AgentIR) RunsAsSubagent() bool {
	return ir.Role == RoleMission || ir.Role == RoleBoth
}

func computeBounds(ir *AgentIR, cfg Config) *hitlservice.ComputeBounds {
	bounds := &hitlservice.ComputeBounds{
		MaxToolCalls:     cfg.Policy.Compute.MaxToolCalls,
		MaxTokens:        cfg.Policy.Compute.MaxTokens,
		OnExhausted:      hitlservice.OnExhausted(cfg.Policy.Compute.OnExhausted),
		ModelAllowlist:   cfg.Policy.Compute.ModelAllowlist,
		BackendAllowlist: cfg.Policy.Compute.BackendAllowlist,
	}
	declared := 0
	if ir.Budgets.MaxTurns != nil && *ir.Budgets.MaxTurns > 0 {
		declared = *ir.Budgets.MaxTurns
	}
	if declared > 0 && declared < bounds.MaxToolCalls {
		bounds.MaxToolCalls = declared
	}
	if ir.RunsAsSubagent() && (cfg.Policy.Compute.MaxTurns == 1 || declared == 1) {
		bounds.MaxTurns = 1
	}
	return bounds
}

func attentionBounds(ir *AgentIR, cfg Config) *hitlservice.AttentionBounds {
	if !ir.RunsAsSubagent() {
		return nil
	}
	a := cfg.Policy.Attention
	if !a.AllowAgentAnswers && !a.AllowAgentApprovals {
		return nil
	}
	return &hitlservice.AttentionBounds{
		AllowAgentAnswers:   a.AllowAgentAnswers,
		MaxAgentAnswers:     a.MaxAgentAnswers,
		AllowAgentApprovals: a.AllowAgentApprovals,
		MaxAgentApprovals:   a.MaxAgentApprovals,
	}
}

func standingRules(in []StandingRule, action hitlservice.Action) []hitlservice.Rule {
	out := make([]hitlservice.Rule, 0, len(in))
	for _, r := range in {
		rule := hitlservice.Rule{Tools: r.Tools, Tool: r.Tool, Action: action}
		if r.WhenKey != "" {
			rule.When = []hitlservice.Condition{{
				Key:   r.WhenKey,
				Op:    hitlservice.ConditionOp(r.WhenOp),
				Value: r.WhenValue,
			}}
		}
		out = append(out, rule)
	}
	return out
}

func parseAction(s string) (hitlservice.Action, error) {
	switch hitlservice.Action(s) {
	case hitlservice.ActionAllow:
		return hitlservice.ActionAllow, nil
	case hitlservice.ActionApprove:
		return hitlservice.ActionApprove, nil
	case hitlservice.ActionDeny:
		return hitlservice.ActionDeny, nil
	default:
		return "", fmt.Errorf("unknown action %q (want allow, approve or deny)", s)
	}
}
