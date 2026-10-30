package acpsvc

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/modelcapability"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

func allACPCommands() []libacp.AvailableCommand {
	return []libacp.AvailableCommand{
		{Name: "help", Description: "List the available commands."},
		{Name: "doctor", Description: "Check provider/model/backend readiness (read-only — no test prompt is sent)."},
		{Name: "clear", Description: "Clear this session's conversation history."},
		{Name: "compact", Description: "Summarize older history into a single message to reclaim context.", Input: &libacp.AvailableCommandInput{Hint: "[keep]"}},
		{Name: "model", Description: "Show the current model, or set it: /model <name>.", Input: &libacp.AvailableCommandInput{Hint: "[model-name]"}},
		{Name: "provider", Description: "Show the current provider, or set it: /provider <name>.", Input: &libacp.AvailableCommandInput{Hint: "[provider-name]"}},
		{Name: "max-tokens", Description: "Show or set the default response token cap: /max-tokens <count>.", Input: &libacp.AvailableCommandInput{Hint: "[count]"}},
		{Name: "think", Description: "Show or set this session's reasoning level: /think <level|off|auto>.", Input: &libacp.AvailableCommandInput{Hint: "[level|off|auto]"}},
		{Name: "capability", Description: "Show or set persistent provider/model capability overrides.", Input: &libacp.AvailableCommandInput{Hint: "set|show|unset <provider> <model> [--think true|false]"}},
		{Name: "policy", Description: "Show the active HITL policy, or switch it: /policy <name>.", Input: &libacp.AvailableCommandInput{Hint: "[policy-name]"}},
		{Name: "mission", Description: "Fire a mission from this session: /mission [agent-name] <intent>.", Input: &libacp.AvailableCommandInput{Hint: "[agent-name] <intent>"}},
		{Name: planCommandName, Description: "Plan a piece of work, then run each step as a subagent.", Input: &libacp.AvailableCommandInput{Hint: "<what you want done>"}},
	}
}

func (t *Transport) acpCommands() []libacp.AvailableCommand {
	all := allACPCommands()
	if t.hasMissionCapability() {
		return all
	}
	out := make([]libacp.AvailableCommand, 0, len(all)-2)
	for _, c := range all {
		if c.Name == "mission" || c.Name == planCommandName {
			continue
		}
		out = append(out, c)
	}
	return out
}

var acpCommandNames = func() map[string]struct{} {
	m := make(map[string]struct{}, len(allACPCommands()))
	for _, c := range allACPCommands() {
		m[c.Name] = struct{}{}
	}
	return m
}()

func parseCommand(input string) (name, args string, ok bool) {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "/") {
		return "", "", false
	}
	rest := s[1:]
	first := rest
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		first = rest[:i]
		args = strings.TrimSpace(rest[i+1:])
	}
	if _, known := acpCommandNames[first]; !known {
		return "", "", false
	}
	return first, args, true
}

func (t *Transport) dispatchCommand(ctx context.Context, sid libacp.SessionID, sess *sessionEntry, name, args string) (libacp.PromptResponse, error) {
	reportErr, _, end := t.tracker().Start(ctx, "command", "acp_session", "session_id", string(sid), "command", name)
	defer end()

	var (
		out string
		err error
	)
	switch name {
	case "help":
		out = t.handleHelp()
	case "doctor":
		out, err = t.handleDoctor(ctx)
	case "model":
		out, err = t.handleModel(ctx, args)
	case "provider":
		out, err = t.handleProvider(ctx, args)
	case "max-tokens":
		out, err = t.handleMaxTokens(ctx, args)
	case "think":
		out, err = t.handleThink(sess, args)
	case "capability":
		out, err = t.handleCapability(ctx, args)
	case "policy":
		out, err = t.handlePolicy(ctx, args)
	case "mission":
		out, err = t.handleMission(ctx, sess, args)
	case "clear":
		out, err = t.handleClear(ctx, sid, sess)
	case "compact":
		out, err = t.handleCompact(ctx, sid, sess, args)
	default:
		err = libacp.NewErrorf(libacp.ErrInvalidParams, "unknown command %q", name)
	}

	if err != nil {
		reportErr(err)
		t.sendUpdate(ctx, libacp.SessionNotification{
			SessionID: sid,
			Update:    libacp.NewAgentMessageChunk("⚠️  " + err.Error()),
		})
		t.persistCommandTurn(ctx, sess, name, args, "⚠️  "+err.Error())
		return libacp.PromptResponse{StopReason: libacp.StopReasonEndTurn}, nil
	}
	if out != "" {
		t.sendUpdate(ctx, libacp.SessionNotification{
			SessionID: sid,
			Update:    libacp.NewAgentMessageChunk(out),
		})
	}
	t.persistCommandTurn(ctx, sess, name, args, out)
	if commandUpdatesSessionModel(name) {
		sess.setModelSelection(t.provider(), t.model())
	}
	if commandUpdatesConfigOptions(name) {
		t.sendConfigOptionUpdate(ctx, sid, sess)
	}
	return libacp.PromptResponse{StopReason: libacp.StopReasonEndTurn}, nil
}

func (t *Transport) persistCommandTurn(ctx context.Context, sess *sessionEntry, name, args, out string) {
	if t.deps.DB == nil || sess == nil || commandRewritesHistory(name) {
		return
	}
	internalID := sess.InternalSessionID
	if internalID == "" || strings.TrimSpace(out) == "" {
		return
	}
	typed := "/" + name
	if args = strings.TrimSpace(args); args != "" {
		typed += " " + args
	}
	now := time.Now().UTC()
	msgs := []taskengine.Message{
		{ID: uuid.NewString(), Role: "user", Content: typed, Timestamp: now},
		{ID: uuid.NewString(), Role: "assistant", Content: out, Timestamp: now.Add(time.Millisecond)},
	}
	cleanCtx := context.WithoutCancel(ctx)
	mgr := chatservice.NewManager(sess.WorkspaceID)
	if err := mgr.PersistDiff(cleanCtx, t.deps.DB.WithoutTransaction(), internalID, msgs); err != nil {
		reportErr, _, end := t.tracker().Start(cleanCtx, "persist", "acp_command_turn", "session_id", internalID, "command", name)
		reportErr(err)
		end()
	}
}

func commandRewritesHistory(name string) bool {
	switch name {
	case "clear", "compact":
		return true
	default:
		return false
	}
}

func commandUpdatesSessionModel(name string) bool {
	switch name {
	case "model", "provider":
		return true
	default:
		return false
	}
}

func commandUpdatesConfigOptions(name string) bool {
	switch name {
	case "model", "provider", "policy", "think":
		return true
	default:
		return false
	}
}

func (t *Transport) sendAvailableCommands(ctx context.Context, sid libacp.SessionID) {
	t.sendUpdate(ctx, libacp.SessionNotification{
		SessionID: sid,
		Update: libacp.SessionUpdate{
			SessionUpdate:     libacp.SessionUpdateAvailableCommands,
			AvailableCommands: t.acpCommands(),
		},
	})
}

func (t *Transport) handleHelp() string {
	cmds := t.acpCommands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	var b strings.Builder
	b.WriteString("Available commands:\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "  /%-9s %s\n", c.Name, c.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *Transport) handleDoctor(ctx context.Context) (string, error) {
	if t.deps.Engine == nil || t.deps.Engine.SetupStatus == nil {
		return "", fmt.Errorf("readiness check unavailable")
	}
	res, err := t.deps.Engine.SetupStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("readiness check failed: %w", err)
	}
	summary := res.Summary()

	ceiling := res.DefaultMaxOutputTokens
	if ceiling > 0 {
		maxTok := t.maxTokens()
		if maxTok != "" {
			if n, convErr := strconv.Atoi(maxTok); convErr == nil && n > ceiling {
				summary += fmt.Sprintf(
					"\n⚠️  Advisory: default-max-tokens=%d exceeds %s provider ceiling (%d). Requests will be clamped automatically.",
					n, t.provider(), ceiling)
			}
		}
	}
	return summary, nil
}

func (t *Transport) handleModel(ctx context.Context, args string) (string, error) {
	value := strings.TrimSpace(args)
	if value == "" {
		return fmt.Sprintf("Model: %s", t.model()), nil
	}
	if provider, model, split := splitModelValue(value); split {
		if _, _, served := resolveModelValue(t.runtimeStates(ctx), value); served {
			if err := t.persistConfig(ctx, "default-provider", provider); err != nil {
				return "", err
			}
			if err := t.persistConfig(ctx, "default-model", model); err != nil {
				return "", err
			}
			t.setProvider(provider)
			t.setModel(model)
			return fmt.Sprintf("Model set to %s (%s).", model, provider), nil
		}
	}
	if err := t.persistConfig(ctx, "default-model", value); err != nil {
		return "", err
	}
	t.setModel(value)
	return fmt.Sprintf("Model set to %s.", value), nil
}

func (t *Transport) handleProvider(ctx context.Context, args string) (string, error) {
	value := strings.TrimSpace(args)
	if value == "" {
		current := t.provider()
		if current == "" {
			return "Provider: (default)", nil
		}
		return fmt.Sprintf("Provider: %s", current), nil
	}
	if err := t.persistConfig(ctx, "default-provider", value); err != nil {
		return "", err
	}
	t.setProvider(value)
	return fmt.Sprintf("Provider set to %s.", value), nil
}

func (t *Transport) handleMaxTokens(ctx context.Context, args string) (string, error) {
	ceiling := t.maxOutputTokensCeiling(ctx)
	value := strings.TrimSpace(args)
	if value == "" {
		current := t.maxTokens()
		if current == "" {
			return fmt.Sprintf("Max tokens: (chain default) | provider ceiling: %s", ceilingLabel(ceiling)), nil
		}
		return fmt.Sprintf("Max tokens: %s | provider ceiling: %s", current, ceilingLabel(ceiling)), nil
	}
	normalized, err := normalizeMaxTokensValue(value)
	if err != nil {
		return "", err
	}
	if err := t.persistConfig(ctx, "default-max-tokens", normalized); err != nil {
		return "", err
	}
	t.setMaxTokens(normalized)
	if normalized == "" {
		return "Max tokens reset to chain default.", nil
	}
	msg := fmt.Sprintf("Max tokens set to %s.", normalized)
	if ceiling > 0 {
		n, _ := strconv.Atoi(normalized)
		if n > ceiling {
			msg += fmt.Sprintf(" ⚠️  Exceeds provider ceiling (%d) — requests will be clamped.", ceiling)
		}
	}
	return msg, nil
}

func (t *Transport) maxOutputTokensCeiling(ctx context.Context) int {
	if t.deps.Engine == nil || t.deps.Engine.State == nil {
		return 0
	}
	states := setupcheck.StatesFromMap(t.deps.Engine.State.Get(ctx))
	return setupcheck.ResolveMaxOutputTokens(states, t.provider(), t.model())
}

func ceilingLabel(ceiling int) string {
	if ceiling > 0 {
		return strconv.Itoa(ceiling)
	}
	return "unknown"
}

func normalizeMaxTokensValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("max-tokens must be a non-negative integer, got %q", value)
	}
	if n < 0 {
		return "", fmt.Errorf("max-tokens must be non-negative, got %d", n)
	}
	return strconv.Itoa(n), nil
}

func (t *Transport) handleThink(sess *sessionEntry, args string) (string, error) {
	value := strings.TrimSpace(args)
	if value == "" {
		return fmt.Sprintf("Think: %s", sess.think()), nil
	}
	level, err := reasoning.Normalize(value)
	if err != nil {
		return "", err
	}
	sess.setThink(level)
	return fmt.Sprintf("Think set to %s for this session.", level), nil
}

func (t *Transport) handleCapability(ctx context.Context, args string) (string, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return t.capabilityUsage(ctx), nil
	}
	switch fields[0] {
	case "show":
		if len(fields) != 3 {
			return "", fmt.Errorf("usage: /capability show <provider> <model>")
		}
		return t.capabilityShow(ctx, fields[1], fields[2])
	case "set":
		provider, model, canThink, err := parseCapabilitySetArgs(fields)
		if err != nil {
			return "", err
		}
		store := runtimetypes.New(t.deps.DB.WithoutTransaction())
		override, err := modelcapability.New(store).SetThink(ctx, provider, model, canThink)
		if err != nil {
			return "", fmt.Errorf("set capability override: %w", err)
		}
		return fmt.Sprintf("Capability override set for %s/%s: think=%t.", override.Provider, override.Model, canThink), nil
	case "unset":
		if len(fields) != 3 {
			return "", fmt.Errorf("usage: /capability unset <provider> <model>")
		}
		store := runtimetypes.New(t.deps.DB.WithoutTransaction())
		removed, err := modelcapability.New(store).Unset(ctx, fields[1], fields[2])
		if err != nil {
			return "", fmt.Errorf("unset capability override: %w", err)
		}
		_, provider, model, keyErr := modelcapability.Key(fields[1], fields[2])
		if keyErr != nil {
			return "", keyErr
		}
		if !removed {
			return fmt.Sprintf("No capability override for %s/%s.", provider, model), nil
		}
		return fmt.Sprintf("Capability override removed for %s/%s.", provider, model), nil
	default:
		return "", fmt.Errorf("usage: /capability set|show|unset <provider> <model> [--think true|false]")
	}
}

func (t *Transport) capabilityUsage(ctx context.Context) string {
	usage := "Usage:\n  /capability show <provider> <model>\n  /capability set <provider> <model> --think true|false\n  /capability unset <provider> <model>\n\nThis persists a provider/model capability override. It is separate from /think, which only changes this session's reasoning level."
	provider := strings.TrimSpace(t.provider())
	model := strings.TrimSpace(t.model())
	if provider == "" || model == "" {
		return usage
	}
	status, err := t.capabilityShow(ctx, provider, model)
	if err != nil {
		return usage
	}
	return usage + "\n\nCurrent default:\n" + status
}

func (t *Transport) capabilityShow(ctx context.Context, provider, model string) (string, error) {
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	override, ok, err := modelcapability.New(store).Get(ctx, provider, model)
	if err != nil {
		return "", fmt.Errorf("show capability override: %w", err)
	}
	if !ok || override.CanThink == nil {
		_, p, m, keyErr := modelcapability.Key(provider, model)
		if keyErr != nil {
			return "", keyErr
		}
		return fmt.Sprintf("No capability override for %s/%s.", p, m), nil
	}
	return fmt.Sprintf("Capability override for %s/%s: think=%t.", override.Provider, override.Model, *override.CanThink), nil
}

func parseCapabilitySetArgs(fields []string) (string, string, bool, error) {
	if len(fields) < 4 {
		return "", "", false, fmt.Errorf("usage: /capability set <provider> <model> --think true|false")
	}
	provider, model := fields[1], fields[2]
	var canThink bool
	seenThink := false
	for i := 3; i < len(fields); i++ {
		arg := fields[i]
		value := ""
		if strings.HasPrefix(arg, "--think=") {
			value = strings.TrimPrefix(arg, "--think=")
		} else if arg == "--think" {
			if i+1 >= len(fields) {
				return "", "", false, fmt.Errorf("--think requires true or false")
			}
			i++
			value = fields[i]
		} else {
			return "", "", false, fmt.Errorf("unknown capability flag %q", arg)
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true":
			canThink = true
		case "false":
			canThink = false
		default:
			return "", "", false, fmt.Errorf("--think must be true or false")
		}
		seenThink = true
	}
	if !seenThink {
		return "", "", false, fmt.Errorf("--think is required")
	}
	return provider, model, canThink, nil
}

func (t *Transport) handlePolicy(ctx context.Context, args string) (string, error) {
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	value := strings.TrimSpace(args)
	if value == "" {
		return t.policyStatus(clikv.ReadHITLPolicy(ctx, store)), nil
	}
	cfgCtx := libtracker.WithNewRequestID(ctx)
	if err := clikv.SetHITLPolicy(cfgCtx, store, value); err != nil {
		return "", fmt.Errorf("set hitl policy: %w", err)
	}
	return fmt.Sprintf("HITL policy set to %s. Applies to the next gated tool call.", value), nil
}

func (t *Transport) policyStatus(active string) string {
	effective := active
	if effective == "" {
		effective = t.deps.HITLDefaultPolicyName
	}
	var b strings.Builder
	if active == "" {
		fmt.Fprintf(&b, "Active HITL policy: %s (default)\n", effective)
	} else {
		fmt.Fprintf(&b, "Active HITL policy: %s\n", effective)
	}
	if len(t.deps.KnownPolicies) > 0 {
		b.WriteString("Presets:\n")
		for _, name := range t.deps.KnownPolicies {
			marker := "  "
			if name == effective {
				marker = "* "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, name)
		}
		b.WriteString("Switch with: /policy <name>")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *Transport) persistConfig(ctx context.Context, key, value string) error {
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	cfgCtx := libtracker.WithNewRequestID(ctx)
	if err := clikv.WriteConfig(cfgCtx, store, t.workspaceID(), key, value); err != nil {
		return fmt.Errorf("persist %s: %w", key, err)
	}
	return nil
}
