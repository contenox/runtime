package acpsvc

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/project"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/statetype"
)

const (
	configIDModel         = "model"
	configIDHITLPolicy    = "hitl-policy"
	configIDThink         = "think"
	configIDTokenLimit    = "token-limit"
	configIDWorkspaceRoot = "workspace-root"

	configCategoryModel         = "model"
	configCategoryHITLPolicy    = "_hitl_policy"
	configCategoryThink         = "thought_level"
	configCategoryWorkspaceRoot = "workspace"

	configTypeSelect = "select"

	hitlPolicyDefaultValue = "__contenox_default__"

	WorkspaceConfigOptionsMetaKey = "contenox.workspaceConfigOptions"
)

func (t *Transport) workspaceConfigOptions(ctx context.Context) []libacp.SessionConfigOption {
	seed := &sessionEntry{
		Provider: t.provider(),
		Model:    t.model(),
		Think:    t.thinkDefault(),
	}
	seed.driver = &nativeDriver{t: t}
	return t.sessionConfigOptions(ctx, seed)
}

func (t *Transport) sessionConfigOptions(ctx context.Context, sess *sessionEntry) []libacp.SessionConfigOption {
	return sess.driver.ConfigOptions(ctx, sess)
}

func (t *Transport) workspaceRootConfigOption(sess *sessionEntry) (libacp.SessionConfigOption, bool) {
	f := t.deps.WorkspaceRoots
	if f == nil {
		return libacp.SessionConfigOption{}, false
	}
	roots := f.Roots()
	if len(roots) == 0 {
		return libacp.SessionConfigOption{}, false
	}
	current := f.Default()
	if sess != nil {
		sess.mu.Lock()
		if sess.Cwd != "" {
			current = sess.Cwd
		}
		sess.mu.Unlock()
	}
	values := make([]libacp.SessionConfigValue, 0, len(roots))
	for _, r := range roots {
		values = append(values, libacp.SessionConfigValue{
			Value:       r,
			Name:        workspaceRootDisplayName(r),
			Description: r,
		})
	}
	return libacp.SessionConfigOption{
		ID:           configIDWorkspaceRoot,
		Name:         "Workspace",
		Description:  "Directory the agent and file explorer operate in for this session.",
		Category:     configCategoryWorkspaceRoot,
		Type:         configTypeSelect,
		CurrentValue: current,
		Options:      libacp.NewSessionConfigValues(values),
	}, true
}

func workspaceRootDisplayName(root string) string {
	return project.DisplayName(root)
}

func (t *Transport) hitlPolicyConfigOption(sess *sessionEntry) libacp.SessionConfigOption {
	return libacp.SessionConfigOption{
		ID:           configIDHITLPolicy,
		Name:         "HITL Policy",
		Description:  "Approval policy used for gated tool calls",
		Category:     configCategoryHITLPolicy,
		Type:         configTypeSelect,
		CurrentValue: sess.hitlPolicy(),
		Options:      t.hitlPolicyConfigValues(sess),
	}
}

func (t *Transport) thinkConfigOption(sess *sessionEntry) libacp.SessionConfigOption {
	return libacp.SessionConfigOption{
		ID:           configIDThink,
		Name:         "Think",
		Description:  "Reasoning level for this session",
		Category:     configCategoryThink,
		Type:         configTypeSelect,
		CurrentValue: sess.think(),
		Options: libacp.NewSessionConfigValues([]libacp.SessionConfigValue{
			{Value: reasoning.Auto, Name: "Auto"},
			{Value: reasoning.Off, Name: "Off"},
			{Value: reasoning.Minimal, Name: "Minimal"},
			{Value: reasoning.Low, Name: "Low"},
			{Value: reasoning.Medium, Name: "Medium"},
			{Value: reasoning.High, Name: "High"},
			{Value: reasoning.XHigh, Name: "XHigh"},
		}),
	}
}

func (t *Transport) tokenLimitConfigOption(ctx context.Context, sess *sessionEntry) libacp.SessionConfigOption {
	limit := sess.effectiveTokenLimit()
	cap := t.modelContextCap(ctx, sess)
	current := "0"
	if cap > 0 {
		// Default is the model maximum; a custom limit above a newly selected
		// smaller model's window falls back to that model's maximum.
		if limit <= 0 || limit > cap {
			limit = cap
		}
	}
	if limit > 0 {
		current = strconv.Itoa(limit)
	}
	desc := "Session context budget (token limit for history). Controls shifting and usage indicator size."
	if cap > 0 {
		desc += fmt.Sprintf(" Defaults to the model's maximum context window (%s).", formatTokenCountShort(cap))
	} else {
		desc += " 0 = chain default / unlimited (the model's context window is not reported)."
	}
	return libacp.SessionConfigOption{
		ID:           configIDTokenLimit,
		Name:         "Token Limit",
		Description:  desc,
		Category:     "context",
		Type:         configTypeSelect,
		CurrentValue: current,
		Options:      tokenLimitConfigValues(cap, limit),
	}
}

// tokenLimitLadder holds the selectable budgets for agentic work. Entries at
// or above the model's reported maximum are dropped.
var tokenLimitLadder = []int{65536, 131072, 262144, 524288, 1048576}

// tokenLimitConfigValues lists the selectable limits. Values are plain
// integer strings; "0" is the chain default. With a reported model maximum
// (cap > 0) the maximum comes first (it is the default) and the ladder is
// capped below it.
func tokenLimitConfigValues(cap, current int) libacp.SessionConfigValues {
	var values []libacp.SessionConfigValue
	if cap > 0 {
		values = append(values, libacp.SessionConfigValue{Value: strconv.Itoa(cap), Name: "Model maximum (" + formatTokenCountShort(cap) + ")", Description: "The model's reported context window"})
	}
	values = append(values, libacp.SessionConfigValue{Value: "0", Name: "Chain default", Description: "Use the chain's token limit (or unlimited)"})
	seen := map[int]struct{}{0: {}}
	if cap > 0 {
		seen[cap] = struct{}{}
	}
	add := func(n int, name, desc string) {
		if n <= 0 {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		values = append(values, libacp.SessionConfigValue{Value: strconv.Itoa(n), Name: name, Description: desc})
	}
	for _, n := range tokenLimitLadder {
		if cap > 0 && n >= cap {
			break
		}
		add(n, formatTokenCountShort(n), "")
	}
	add(current, formatTokenCountShort(current)+" (current)", "Session's current custom budget")
	return libacp.NewSessionConfigValues(values)
}

// formatTokenCountShort renders 400000 as "400k tokens", 1048576 as "1M tokens".
func formatTokenCountShort(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.Itoa(n>>20) + "M tokens"
	case n >= 1_000_000 && n%1_000_000 == 0:
		return strconv.Itoa(n/1_000_000) + "M tokens"
	case n >= 1024 && n%1024 == 0:
		return strconv.Itoa(n/1024) + "k tokens"
	case n >= 1000 && n%1000 == 0:
		return strconv.Itoa(n/1000) + "k tokens"
	}
	return strconv.Itoa(n) + " tokens"
}

func (t *Transport) modelContextCap(ctx context.Context, sess *sessionEntry) int {
	if sess == nil {
		return 0
	}
	return contextCapFor(t.runtimeStates(ctx), sess.providerOrDefault(t.provider()), sess.modelOrDefault(t.model()))
}

// contextCapFor returns the context window reported for the model (catalog /
// provider metadata, e.g. the ChatGPT models list's context_window), or 0.
func contextCapFor(states []statetype.BackendRuntimeState, prov, mod string) int {
	mod = strings.TrimSpace(mod)
	if mod == "" {
		return 0
	}
	min := func(matches []statetype.BackendRuntimeState) int {
		best := 0
		for _, st := range matches {
			for _, pm := range st.PulledModels {
				if sameModelName(servedModelName(pm), mod) && pm.ContextLength > 0 && (best == 0 || pm.ContextLength < best) {
					best = pm.ContextLength
				}
			}
		}
		return best
	}
	// any/"" and connection:ID selectors; the minimum is the safe budget when
	// several connections serve the model.
	if n := min(serving(states, prov, mod)); n > 0 {
		return n
	}
	return min(serving(states, "", mod))
}

func (t *Transport) hitlPolicyConfigValues(sess *sessionEntry) libacp.SessionConfigValues {
	defaultName := "Default"
	defaultDescription := "Use Contenox's configured fallback policy"
	if name := strings.TrimSpace(t.deps.HITLDefaultPolicyName); name != "" {
		defaultDescription = "Use " + name
	}
	values := []libacp.SessionConfigValue{{
		Value:       hitlPolicyDefaultValue,
		Name:        defaultName,
		Description: defaultDescription,
	}}

	seen := map[string]struct{}{hitlPolicyDefaultValue: {}}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		values = append(values, libacp.SessionConfigValue{Value: name, Name: hitlPolicyDisplayName(name)})
	}
	for _, name := range t.deps.KnownPolicies {
		add(name)
	}
	add(sess.hitlPolicy())
	return libacp.NewSessionConfigValues(values)
}

func (t *Transport) resolveSessionHITLPolicy(sess *sessionEntry) string {
	name := sess.hitlPolicy()
	if name == "" || name == hitlPolicyDefaultValue {
		return strings.TrimSpace(t.deps.HITLDefaultPolicyName)
	}
	return name
}

func (t *Transport) runtimeStates(ctx context.Context) []statetype.BackendRuntimeState {
	if t.statesFn != nil {
		return t.statesFn()
	}
	if t.deps.Engine == nil || t.deps.Engine.State == nil {
		return nil
	}
	_ = t.deps.Engine.State.ReconcileIfStale(ctx)
	states := t.deps.Engine.State.Get(ctx)
	out := make([]statetype.BackendRuntimeState, 0, len(states))
	for _, state := range states {
		out = append(out, state)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i].Backend.Type + "/" + out[i].Name)
		right := strings.ToLower(out[j].Backend.Type + "/" + out[j].Name)
		return left < right
	})
	return out
}

func (t *Transport) SetSessionConfigOption(ctx context.Context, req libacp.SetSessionConfigOptionRequest) (libacp.SetSessionConfigOptionResponse, error) {
	reportErr, reportChange, end := t.tracker().Start(ctx, "set_config_option", "acp_session", "session_id", string(req.SessionID), "config_id", req.ConfigID)
	defer end()

	sess, ok := t.sessionFor(req.SessionID)
	if !ok {
		err := libacp.NewErrorf(libacp.ErrInvalidParams, "unknown session %q", req.SessionID)
		reportErr(err)
		return libacp.SetSessionConfigOptionResponse{}, err
	}

	if err := sess.driver.SetConfigOption(ctx, sess, req.ConfigID, req.Value); err != nil {
		reportErr(err)
		return libacp.SetSessionConfigOptionResponse{}, err
	}

	reportChange(req.ConfigID, req.Value.AsString())
	return libacp.SetSessionConfigOptionResponse{
		ConfigOptions: t.sessionConfigOptions(ctx, sess),
	}, nil
}

func (t *Transport) setSessionConfigOption(ctx context.Context, sess *sessionEntry, configID, value string) error {
	switch configID {
	case configIDModel:
		provider, model, ok := resolveModelValue(t.runtimeStates(ctx), value)
		if !ok {
			return libacp.NewErrorf(libacp.ErrInvalidParams, "unknown model option %q (expected any/<model> or connection:<id>/<model>)", value)
		}
		sess.setModelSelection(provider, model)
		return nil

	case configIDHITLPolicy:
		if !configOptionHasValue(t.hitlPolicyConfigOption(sess), value) {
			return libacp.NewErrorf(libacp.ErrInvalidParams, "unknown HITL policy option %q", value)
		}
		sess.setHITLPolicy(value)
		return nil

	case configIDThink:
		level, err := reasoning.Normalize(value)
		if err != nil {
			return libacp.NewError(libacp.ErrInvalidParams, err.Error())
		}
		if !configOptionHasValue(t.thinkConfigOption(sess), level) {
			return libacp.NewErrorf(libacp.ErrInvalidParams, "unknown think option %q", value)
		}
		sess.setThink(level)
		return nil

	case configIDTokenLimit:
		requested := 0
		if strings.TrimSpace(value) != "" && value != "0" {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return libacp.NewErrorf(libacp.ErrInvalidParams, "token-limit must be a non-negative integer, got %q", value)
			}
			requested = n
		}
		cap := t.modelContextCap(ctx, sess)
		eff := requested
		if cap > 0 && eff > cap {
			eff = cap
		}
		sess.setEffectiveTokenLimit(eff)
		return nil

	case configIDWorkspaceRoot:
		return libacp.NewErrorf(libacp.ErrInvalidParams, "the workspace cannot be changed after the session starts")

	default:
		return libacp.NewErrorf(libacp.ErrInvalidParams, "unknown config option %q", configID)
	}
}

func (t *Transport) sendConfigOptionUpdate(ctx context.Context, sid libacp.SessionID, sess *sessionEntry) {
	if t.conn == nil || sess == nil {
		return
	}
	t.sendUpdate(ctx, libacp.SessionNotification{
		SessionID: sid,
		Update: libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateConfigOption,
			ConfigOptions: t.sessionConfigOptions(ctx, sess),
		},
	})
}

func configOptionHasValue(option libacp.SessionConfigOption, value string) bool {
	for _, candidate := range option.Options.AllValues() {
		if candidate.Value == value {
			return true
		}
	}
	return false
}

func modelConfigValue(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	return provider + "/" + model
}

func modelConfigDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "(default)"
	}
	return strings.TrimPrefix(model, "models/")
}

func splitModelConfigValue(value string) (provider, model string) {
	value = strings.TrimSpace(value)
	if before, after, ok := strings.Cut(value, "/"); ok {
		return strings.TrimSpace(before), strings.TrimSpace(after)
	}
	return "", value
}

func hitlPolicyDisplayName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".json")
	name = strings.TrimPrefix(name, "hitl-policy-")
	if name == "" {
		return "Default"
	}
	return name
}

func describePulledModel(model statetype.ModelPullStatus) string {
	var parts []string
	if model.ContextLength > 0 {
		parts = append(parts, "context "+strconv.Itoa(model.ContextLength))
	}
	if model.MaxOutputTokens > 0 {
		parts = append(parts, "output ceiling "+strconv.Itoa(model.MaxOutputTokens))
	}
	if model.CanThink {
		parts = append(parts, "thinking")
	}
	return strings.Join(parts, ", ")
}

// requireDefaultModel fails a turn up front when no model is configured, with
// the same code/wording the setup check reports (missing_default_model).
func requireDefaultModel(vars map[string]string) error {
	if strings.TrimSpace(vars["model"]) != "" {
		return nil
	}
	return fmt.Errorf("missing_default_model: No default model is set. Internal chat and chains using {{var:model}} need it. Fix: contenox config set default-model <name>")
}
