package contenoxcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_SeededPolicies_StateWhoMayAnswer(t *testing.T) {
	dir := t.TempDir()
	for _, p := range shippedHITLPolicies(t) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, p.Name), []byte(p.Content), 0o600))
	}
	svc := hitlservice.New(hitlservice.NewFSPolicySource(dir), runtimetypes.LocalTenantID, nil, libtracker.NoopTracker{})

	for _, tc := range []struct {
		policy    string
		mayAnswer bool
		cap       int
		why       string
	}{
		{"hitl-policy-acp.json", true, 3, "an editor session's agent holds the conversation the mission came from"},
		{"hitl-policy-default.json", true, 2, "routine questions, with whatever the unit then does still gated"},
		{"hitl-policy-dev.json", true, 5, "the permissive local-development posture"},
		{"hitl-policy-strict.json", false, 0, "a policy whose character is 'a human decides' must not delegate deciding"},
		{"hitl-policy-acpx.json", false, 0, "an untrusted driver's agent must not answer its own subagent"},
	} {
		bounds, err := svc.AttentionBoundsFor(context.Background(), tc.policy)
		require.NoErrorf(t, err, "%s must load", tc.policy)
		require.Equalf(t, tc.mayAnswer, bounds.AllowAgentAnswers, "%s: %s", tc.policy, tc.why)
		if tc.mayAnswer {
			require.Equalf(t, tc.cap, bounds.EffectiveMaxAgentAnswers(), "%s: an allowed preset still names its cap", tc.policy)
		}
	}
}

func TestUnit_SeededPolicies_WithoutAnAttentionBlockOnlyAHumanAnswers(t *testing.T) {
	dir := t.TempDir()
	for _, p := range shippedHITLPolicies(t) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, p.Name), []byte(p.Content), 0o600))
	}
	svc := hitlservice.New(hitlservice.NewFSPolicySource(dir), runtimetypes.LocalTenantID, nil, libtracker.NoopTracker{})
	for _, p := range shippedHITLPolicies(t) {
		var doc map[string]any
		require.NoErrorf(t, json.Unmarshal([]byte(p.Content), &doc), "%s must be valid JSON", p.Name)
		if _, stated := doc["attention"]; stated {
			continue
		}
		bounds, err := svc.AttentionBoundsFor(context.Background(), p.Name)
		require.NoError(t, err)
		require.Falsef(t, bounds.AllowAgentAnswers, "%s states no attention block, so no agent may answer for it", p.Name)
		require.Falsef(t, bounds.AllowAgentApprovals, "%s states no attention block, so no agent may approve for it", p.Name)
	}
}
