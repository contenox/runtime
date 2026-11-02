package hitlservice_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/require"
)

func writeChoicePolicy(t *testing.T, dir, file, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600))
}

func TestUnit_PolicyChoices_OneListForEveryPicker(t *testing.T) {
	data := t.TempDir()
	generated := filepath.Join(data, hitlservice.GeneratedPolicyDir)
	home := t.TempDir()

	writeChoicePolicy(t, generated, "hitl-policy-strict.json", `{"//": "Refuse what is not named. Rendered from [envelopes.strict] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, generated, "hitl-policy-default.json", `{"//": "The default envelope. Rendered from [envelopes.default] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, generated, "hitl-policy-chat.json", `{"version": 1, "rules": []}`)
	writeChoicePolicy(t, data, "hitl-policy-mine.json", `{"//": "My own rules.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, data, "hitl-policy-strict.json", `{"version": 1, "rules": []}`)
	writeChoicePolicy(t, data, "notes.json", `{}`)
	writeChoicePolicy(t, home, "hitl-policy-team.json", `{"version": 1, "rules": []}`)

	choices := hitlservice.Choices(hitlservice.NewFSPolicySource(generated, data, home))
	require.Equal(t, []hitlservice.PolicyChoice{
		{Name: "default", File: "hitl-policy-default.json", Description: "The default envelope.", Source: hitlservice.PolicyFromEnvelope},
		{Name: "mine", File: "hitl-policy-mine.json", Description: "My own rules.", Source: hitlservice.PolicyFromFile},
		{Name: "strict", File: "hitl-policy-strict.json", Description: "Refuse what is not named.", Source: hitlservice.PolicyFromEnvelope},
		{Name: "team", File: "hitl-policy-team.json", Source: hitlservice.PolicyFromFile},
	}, choices, "envelopes and hand-written files are choices, default first; an agent's posture policy is not; the folder that wins resolution wins the listing")
}

func TestUnit_PolicyChoices_SourceWithoutAListingHasNoChoices(t *testing.T) {
	require.Nil(t, hitlservice.Choices(nil))
}

func TestUnit_PolicyChoices_AWorkspaceSeesItsOwnEnvelopesOverTheGlobalOnes(t *testing.T) {
	data := t.TempDir()
	generated := filepath.Join(data, hitlservice.GeneratedPolicyDir)
	writeChoicePolicy(t, generated, "hitl-policy-default.json", `{"//": "Global default. Rendered from [envelopes.default] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, generated, "hitl-policy-strict.json", `{"//": "Global strict. Rendered from [envelopes.strict] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, filepath.Join(generated, "workspaces", "ws-1"), "hitl-policy-strict.json", `{"//": "Strict here. Rendered from [envelopes.strict] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, filepath.Join(generated, "workspaces", "ws-1"), "hitl-policy-local.json", `{"//": "Only here. Rendered from [envelopes.local] in agents.toml.", "version": 1, "rules": []}`)
	writeChoicePolicy(t, filepath.Join(generated, "workspaces", "ws-2"), "hitl-policy-other.json", `{"//": "Elsewhere. Rendered from [envelopes.other] in agents.toml.", "version": 1, "rules": []}`)
	src := hitlservice.NewFSPolicySource(generated, data)

	got := hitlservice.ChoicesFor(src, "ws-1")
	require.Equal(t, []hitlservice.PolicyChoice{
		{Name: "default", File: "hitl-policy-default.json", Description: "Global default.", Source: hitlservice.PolicyFromEnvelope},
		{Name: "local", File: "hitl-policy-local.json", Description: "Only here.", Source: hitlservice.PolicyFromEnvelope, Scope: hitlservice.ScopeWorkspace},
		{Name: "strict", File: "hitl-policy-strict.json", Description: "Strict here.", Source: hitlservice.PolicyFromEnvelope, Scope: hitlservice.ScopeWorkspace},
	}, got, "a workspace's envelope replaces the global one of the same name and adds its own; another workspace's never shows")
	require.Equal(t, hitlservice.Choices(src), hitlservice.ChoicesFor(src, ""), "no workspace, the global list")

	require.Equal(t, "workspaces/ws-1/hitl-policy-strict.json", hitlservice.ResolveForWorkspace(src, "ws-1", "strict"))
	require.Equal(t, "workspaces/ws-1/hitl-policy-strict.json", hitlservice.ResolveForWorkspace(src, "ws-1", "hitl-policy-strict.json"))
	require.Equal(t, "default", hitlservice.ResolveForWorkspace(src, "ws-1", "default"), "unchanged names stay as they are")
	require.Equal(t, "strict", hitlservice.ResolveForWorkspace(src, "ws-2", "strict"))
	require.Equal(t, "strict", hitlservice.ResolveForWorkspace(src, "", "strict"))

	body, err := src.ReadPolicy(context.Background(), "", hitlservice.ResolveForWorkspace(src, "ws-1", "strict"))
	require.NoError(t, err)
	require.Contains(t, string(body), "Strict here.")
	_, err = src.ReadPolicy(context.Background(), "", "workspaces/../../etc/hitl-policy-x.json")
	require.Error(t, err, "a policy name may not climb out of the policy folders")
}
