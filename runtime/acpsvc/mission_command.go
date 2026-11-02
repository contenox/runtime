package acpsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/chainagents"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	missionCommand         = "mission"
	missionAgentCommand    = "mission-agent"
	missionEnvelopeCommand = "mission-envelope"

	missionAgentConfigKey  = "default-mission-agent"
	missionPolicyConfigKey = "default-mission-policy"

	missionPolicyFlag = "policy"
	missionUsageLine  = "usage: /mission [--policy <envelope>] [agent-name] <intent>"
)

var missionCommandNames = map[string]bool{
	missionCommand:         true,
	missionAgentCommand:    true,
	missionEnvelopeCommand: true,
}

type missionFlags struct {
	policy string
}

func parseMissionFlags(args string) (missionFlags, string, error) {
	var f missionFlags
	rest := strings.TrimSpace(args)
	for {
		token, after := splitFirstToken(rest)
		if !strings.HasPrefix(token, "--") {
			return f, rest, nil
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(token, "--"), "=")
		if name != missionPolicyFlag {
			return f, "", fmt.Errorf("unknown /mission flag %q: the only flag is --policy <envelope>, and it must come before the agent and intent (%s)", token, missionUsageLine)
		}
		if !hasValue {
			value, after = splitFirstToken(after)
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "--") {
			return f, "", fmt.Errorf("--policy needs an envelope name: %s — /mission with no arguments lists the envelopes", missionUsageLine)
		}
		f.policy = value
		rest = after
	}
}

func missionChoices(choices []string) string {
	if len(choices) == 0 {
		return "None are available yet: restart contenox-runtime serve so it seeds them."
	}
	return "Available: " + strings.Join(choices, ", ") + "."
}

func missionFixHint(command, configKey, placeholder string, choices []string) string {
	example := placeholder
	if len(choices) > 0 {
		example = choices[0]
	}
	return fmt.Sprintf("Fix it in this chat with `/%s %s`. %s Or choose one under Settings → Missions, or run `contenox-runtime config set %s %s`.",
		command, example, missionChoices(choices), configKey, placeholder)
}

func (t *Transport) missionAgentNames(ctx context.Context) []string {
	if t.deps.DB == nil {
		return nil
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	var names []string
	var cursor *time.Time
	for {
		page, err := store.ListAgents(ctx, cursor, runtimetypes.MAXLIMIT)
		if err != nil || len(page) == 0 {
			break
		}
		for _, a := range page {
			if a.Enabled && chainagents.MissionCapable(a) {
				names = append(names, a.Name)
			}
		}
		if len(page) < runtimetypes.MAXLIMIT {
			break
		}
		cursor = &page[len(page)-1].CreatedAt
	}
	sort.Strings(names)
	return names
}

func (t *Transport) missionEnvelopeNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, dir := range t.deps.MissionPolicyDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, "hitl-policy-") || !strings.HasSuffix(name, ".json") || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		for _, name := range t.deps.KnownPolicies {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func matchEnvelope(envelopes []string, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	candidates := []string{name}
	if !strings.HasSuffix(name, ".json") {
		candidates = append(candidates, "hitl-policy-"+strings.TrimPrefix(name, "hitl-policy-")+".json")
	}
	for _, c := range candidates {
		for _, e := range envelopes {
			if e == c {
				return e, true
			}
		}
	}
	if len(envelopes) == 0 {
		return name, true
	}
	return "", false
}

func (t *Transport) missionCompletionMeta(ctx context.Context, name string) json.RawMessage {
	completions := map[string]libacp.CommandCompletion{}
	switch name {
	case missionCommand:
		completions[""] = libacp.CommandCompletion{
			Values: t.missionAgentNames(ctx),
			Hint:   "Choose a mission agent, then type the intent; or type an intent for the default agent. Choosing never dispatches.",
		}
		completions["--policy"] = libacp.CommandCompletion{
			Values: t.missionEnvelopeNames(),
			Hint:   "Choose the envelope that bounds this one mission, then an agent and the intent.",
		}
	case missionAgentCommand:
		completions[""] = libacp.CommandCompletion{
			Values: t.missionAgentNames(ctx),
			Hint:   "Choose the default agent for /mission. Without a name, shows the current default.",
		}
	case missionEnvelopeCommand:
		completions[""] = libacp.CommandCompletion{
			Values: t.missionEnvelopeNames(),
			Hint:   "Choose the default envelope for /mission. Without a name, shows the current default.",
		}
	default:
		return nil
	}
	raw, err := json.Marshal(libacp.CommandCompletionMeta{Completions: completions})
	if err != nil {
		return nil
	}
	return raw
}

func (t *Transport) missionStatus(ctx context.Context, store runtimetypes.Store) string {
	var b strings.Builder
	b.WriteString("Fire a mission from this session:\n")
	b.WriteString("  /mission <intent>                              the default agent under the default envelope\n")
	b.WriteString("  /mission <agent-name> <intent>                 a named mission agent\n")
	b.WriteString("  /mission --policy <envelope> [agent] <intent>  bound this one mission by another envelope\n")

	agent := clikv.Read(ctx, store, missionAgentConfigKey)
	if agent == "" {
		agent = "(none)"
	}
	envelope := clikv.Read(ctx, store, missionPolicyConfigKey)
	if envelope == "" {
		envelope = "(none)"
	}
	fmt.Fprintf(&b, "\nDefault agent:    %s\nDefault envelope: %s\n", agent, envelope)

	writeList := func(title string, names []string, current string) {
		fmt.Fprintf(&b, "\n%s:\n", title)
		if len(names) == 0 {
			b.WriteString("  (none available yet: restart contenox-runtime serve so it seeds them)\n")
			return
		}
		for _, n := range names {
			marker := "  "
			if n == current {
				marker = "* "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, n)
		}
	}
	writeList("Mission agents", t.missionAgentNames(ctx), agent)
	writeList("Envelopes (--policy <name>)", t.missionEnvelopeNames(), envelope)

	b.WriteString("\nChange the defaults with /mission-agent <name> and /mission-envelope <name>, under Settings → Missions, or with `contenox-runtime config set default-mission-agent <name>` / `contenox-runtime config set default-mission-policy <envelope>`.")
	return b.String()
}

func (t *Transport) requireMissionCapability(command string) error {
	if t.hasMissionCapability() || t.deps.MissionForwarded != nil {
		return nil
	}
	return fmt.Errorf("/%s is unavailable in this session: missions need a configured model and the in-process fleet. Configure a model with `contenox-runtime config set default-model …`, or set CONTENOX_SERVER_URL to fire onto a running serve", command)
}

func (t *Transport) handleMissionAgent(ctx context.Context, args string) (string, error) {
	if err := t.requireMissionCapability(missionAgentCommand); err != nil {
		return "", err
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	agents := t.missionAgentNames(ctx)
	name := strings.TrimSpace(args)
	if name == "" {
		current := clikv.Read(ctx, store, missionAgentConfigKey)
		if current == "" {
			current = "(none)"
		}
		return fmt.Sprintf("Default mission agent: %s\n%s\nChange it with `/%s <name>`, under Settings → Missions, or with `contenox-runtime config set %s <name>`.",
			current, missionChoices(agents), missionAgentCommand, missionAgentConfigKey), nil
	}
	if strings.ContainsAny(name, " \t\n") {
		return "", fmt.Errorf("usage: /%s [name] — one agent name. %s", missionAgentCommand, missionChoices(agents))
	}
	found := false
	for _, a := range agents {
		if a == name {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%q is not an enabled mission agent. %s", name, missionFixHint(missionAgentCommand, missionAgentConfigKey, "<name>", agents))
	}
	if err := t.persistConfig(ctx, missionAgentConfigKey, name); err != nil {
		return "", err
	}
	return fmt.Sprintf("Default mission agent set to %s. /mission <intent> now fires it.", name), nil
}

func (t *Transport) handleMissionEnvelope(ctx context.Context, args string) (string, error) {
	if err := t.requireMissionCapability(missionEnvelopeCommand); err != nil {
		return "", err
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	envelopes := t.missionEnvelopeNames()
	name := strings.TrimSpace(args)
	if name == "" {
		current := clikv.Read(ctx, store, missionPolicyConfigKey)
		if current == "" {
			current = "(none)"
		}
		return fmt.Sprintf("Default mission envelope: %s\n%s\nChange it with `/%s <envelope>`, under Settings → Missions, or with `contenox-runtime config set %s <envelope>`.",
			current, missionChoices(envelopes), missionEnvelopeCommand, missionPolicyConfigKey), nil
	}
	if strings.ContainsAny(name, " \t\n") {
		return "", fmt.Errorf("usage: /%s [envelope] — one envelope name. %s", missionEnvelopeCommand, missionChoices(envelopes))
	}
	resolved, ok := matchEnvelope(envelopes, name)
	if !ok {
		return "", fmt.Errorf("%q is not a known mission envelope. %s", name, missionFixHint(missionEnvelopeCommand, missionPolicyConfigKey, "<envelope>", envelopes))
	}
	if err := t.persistConfig(ctx, missionPolicyConfigKey, resolved); err != nil {
		return "", err
	}
	return fmt.Sprintf("Default mission envelope set to %s. /mission now runs under it unless --policy names another.", resolved), nil
}
