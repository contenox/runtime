package contenoxcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/require"
)

type presetKV struct{ name string }

func (p presetKV) GetKV(_ context.Context, _ string, out interface{}) error {
	value, ok := out.(*string)
	if !ok {
		return fmt.Errorf("presetKV: unexpected target %T", out)
	}
	*value = p.name
	return nil
}

func decodePreset(t *testing.T, name, content string) hitlservice.Policy {
	t.Helper()
	var policy hitlservice.Policy
	require.NoError(t, json.Unmarshal([]byte(content), &policy), name)
	return policy
}

func conditionText(rule hitlservice.Rule) string {
	var b strings.Builder
	for _, condition := range rule.When {
		b.WriteString(condition.Value)
	}
	return b.String()
}

func TestUnit_SeededPoliciesQuarantineCredentialsBeforeAnyAllow(t *testing.T) {
	for _, preset := range HITLPolicyPresets {
		if preset.Name == "hitl-policy-dev.json" || preset.Name == "hitl-policy-oracle.json" {
			continue
		}
		policy := decodePreset(t, preset.Name, preset.Content)

		lastGuard, firstAllow := -1, -1
		for i, rule := range policy.Rules {
			if rule.Tools != "local_fs" {
				continue
			}
			if rule.Action == hitlservice.ActionDeny && strings.Contains(conditionText(rule), ".ssh") {
				lastGuard = i
			}
			if rule.Action == hitlservice.ActionAllow && firstAllow < 0 {
				firstAllow = i
			}
		}
		require.NotEqual(t, -1, lastGuard, "%s must deny credential stores itself", preset.Name)
		require.NotEqual(t, -1, firstAllow, "%s must declare its filesystem reads", preset.Name)
		require.Greater(t, firstAllow, lastGuard, "%s: every credential guard must precede the first allow", preset.Name)
	}
}

func TestUnit_SeededPoliciesDenyWritesUnderGit(t *testing.T) {
	for _, name := range []string{
		"hitl-policy-default.json",
		"hitl-policy-strict.json",
		"hitl-policy-acp.json",
	} {
		var policy hitlservice.Policy
		found := false
		for _, preset := range HITLPolicyPresets {
			if preset.Name != name {
				continue
			}
			policy = decodePreset(t, preset.Name, preset.Content)
			found = true
			break
		}
		require.True(t, found, "preset %s not found", name)

		deniesGit := false
		for _, rule := range policy.Rules {
			if rule.Tools != "local_fs" || rule.Action != hitlservice.ActionDeny {
				continue
			}
			if rule.Tool != "write_file" && rule.Tool != "sed" {
				continue
			}
			if strings.Contains(conditionText(rule), ".git/") {
				deniesGit = true
			}
		}
		require.True(t, deniesGit, "%s must refuse writes under .git", name)
	}
}

func TestUnit_SeededPoliciesBoundCompute(t *testing.T) {
	for _, preset := range HITLPolicyPresets {
		policy := decodePreset(t, preset.Name, preset.Content)
		require.NotNil(t, policy.Compute, "%s must carry compute bounds", preset.Name)
		require.Equal(t, hitlservice.OnExhaustedFinishStuck, policy.Compute.OnExhausted, "%s onExhausted", preset.Name)
		if preset.Name == "hitl-policy-oracle.json" {
			continue
		}
		require.Positive(t, policy.Compute.MaxToolCalls, "%s maxToolCalls", preset.Name)
		require.Positive(t, policy.Compute.MaxTokens, "%s maxTokens", preset.Name)
	}
}

func TestUnit_SeededDefaultPolicyRefusesCredentialAndGitWrites(t *testing.T) {
	var preset struct {
		Name    string
		Content string
	}
	for _, candidate := range HITLPolicyPresets {
		if candidate.Name == "hitl-policy-default.json" {
			preset.Name, preset.Content = candidate.Name, candidate.Content
			break
		}
	}
	require.NotEmpty(t, preset.Name)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, preset.Name), []byte(preset.Content), 0o600))
	svc := hitlservice.New(
		hitlservice.NewFSPolicySource(dir),
		"test-tenant",
		presetKV{name: preset.Name},
		libtracker.NoopTracker{},
	)
	ctx := context.Background()

	secret, err := svc.Evaluate(ctx, "local_fs", "read_file", map[string]any{"path": "/home/u/.ssh/id_rsa"})
	require.NoError(t, err)
	require.Equal(t, hitlservice.ActionDeny, secret.Action, "the shipped default must refuse credential stores")

	gitWrite, err := svc.Evaluate(ctx, "local_fs", "write_file", map[string]any{"path": "/repo/.git/hooks/pre-commit"})
	require.NoError(t, err)
	require.Equal(t, hitlservice.ActionDeny, gitWrite.Action, "the shipped default must refuse .git writes")

	normal, err := svc.Evaluate(ctx, "local_fs", "read_file", map[string]any{"path": "/repo/README.md"})
	require.NoError(t, err)
	require.Equal(t, hitlservice.ActionAllow, normal.Action, "ordinary reads stay allowed")
}
