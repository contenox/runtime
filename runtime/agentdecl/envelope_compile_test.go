package agentdecl_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const envelopeTestTenant = "00000000-0000-0000-0000-000000000001"

type activePolicyName struct{ name string }

func (f activePolicyName) GetKV(_ context.Context, _ string, out any) error {
	if p, ok := out.(*string); ok {
		*p = f.name
	}
	return nil
}

func compileEnvelope(t *testing.T, cfg agentdecl.Config, name string) *hitlservice.Policy {
	t.Helper()
	ir := &agentdecl.AgentIR{
		Name:    "envelope-probe",
		Posture: agentdecl.Posture(name),
		Source:  agentdecl.Source{Dialect: agentdecl.DialectClaudeCode},
	}
	policy, err := agentdecl.EmitPolicy(ir, cfg)
	require.NoError(t, err)
	require.NotNil(t, policy)
	return policy
}

func envelopeEvaluator(t *testing.T, cfg agentdecl.Config, name string) hitlservice.PolicyEvaluator {
	t.Helper()
	policy := compileEnvelope(t, cfg, name)
	raw, err := json.Marshal(policy)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hitl-policy.json"), raw, 0o644))
	return hitlservice.New(
		hitlservice.NewFSPolicySource(dir),
		envelopeTestTenant,
		activePolicyName{"hitl-policy.json"},
		libtracker.NoopTracker{},
	)
}

func evaluate(t *testing.T, svc hitlservice.PolicyEvaluator, tools, tool string, args map[string]any) hitlservice.Action {
	t.Helper()
	got, err := svc.Evaluate(context.Background(), tools, tool, args)
	require.NoError(t, err)
	return got.Action
}

func TestUnit_Envelopes_EveryShippedEnvelopeCompilesAndLoads(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	names := cfg.EnvelopeNames()
	require.NotEmpty(t, names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			policy := compileEnvelope(t, cfg, name)
			assert.Equal(t, hitlservice.PolicySchemaVersion, policy.Version, "a compiled policy states its wire version")
			assert.NotEmpty(t, policy.DefaultAction, "every envelope names a default action")
			assert.NotEmpty(t, policy.Rules, "every envelope compiles to at least one rule")

			svc := envelopeEvaluator(t, cfg, name)
			_ = evaluate(t, svc, "local_fs", "read_file", map[string]any{"path": "main.go"})
		})
	}
}

func TestUnit_Envelopes_CredentialQuarantineSurvivesEveryEnvelope(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	for _, name := range []string{"read_only", "ask_always", "auto_edit", "default", "acpx"} {
		t.Run(name, func(t *testing.T) {
			svc := envelopeEvaluator(t, cfg, name)
			for _, path := range []string{
				"/home/dev/.ssh/id_rsa",
				"/home/dev/.aws/credentials",
				"/home/dev/.gnupg/private.key",
				"/home/dev/AppData/Roaming/Microsoft/Credentials/token",
				"/home/dev/AppData/Roaming/Microsoft/Windows/PowerShell/PSReadLine/ConsoleHost_history.txt",
			} {
				got := evaluate(t, svc, "local_fs", "read_file", map[string]any{"path": path})
				assert.Equal(t, hitlservice.ActionDeny, got, "%s must stay quarantined under %s", path, name)
			}
		})
	}
}

func TestUnit_Envelopes_ReadOnlyChangesNothing(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	svc := envelopeEvaluator(t, cfg, "read_only")

	assert.Equal(t, hitlservice.ActionAllow, evaluate(t, svc, "local_fs", "read_file", map[string]any{"path": "main.go"}))
	assert.Equal(t, hitlservice.ActionDeny, evaluate(t, svc, "local_fs", "write_file", map[string]any{"path": "main.go", "content": "x"}))
	assert.Equal(t, hitlservice.ActionDeny, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "ls"}))
}

func TestUnit_Envelopes_DefaultShellTiers(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	svc := envelopeEvaluator(t, cfg, "default")

	assert.Equal(t, hitlservice.ActionAllow, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "go test ./..."}))
	assert.Equal(t, hitlservice.ActionApprove, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "rm -rf build"}))
	assert.Equal(t, hitlservice.ActionApprove, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "go test; rm -rf build"}),
		"an allowlisted reader carrying a second command must not stay allowed")
	assert.Equal(t, hitlservice.ActionApprove, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "go test > out.txt"}),
		"a redirect turns a reader into a writer")
	assert.Equal(t, hitlservice.ActionAllow, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "Get-Content main.go"}))
	assert.Equal(t, hitlservice.ActionApprove, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "Remove-Item build -Recurse"}))
	assert.Equal(t, hitlservice.ActionApprove, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "Get-Content main.go; Remove-Item build"}),
		"a PowerShell reader carrying a second command must not stay allowed")
}

func TestUnit_Envelopes_StrictRefusesWhatItDoesNotName(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	svc := envelopeEvaluator(t, cfg, "strict")

	assert.Equal(t, hitlservice.ActionDeny, evaluate(t, svc, "unknown_toolset", "some_tool", map[string]any{"x": "y"}))
}

func TestUnit_Envelopes_OracleOnlyReachesTheVerdict(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	svc := envelopeEvaluator(t, cfg, "oracle")

	assert.Equal(t, hitlservice.ActionAllow, evaluate(t, svc, "oracle", "submit", map[string]any{"verdict": "pass"}))
	assert.Equal(t, hitlservice.ActionDeny, evaluate(t, svc, "local_fs", "read_file", map[string]any{"path": "main.go"}))
	assert.Equal(t, hitlservice.ActionDeny, evaluate(t, svc, "local_shell", "local_shell", map[string]any{"command": "ls"}))
}
