package contenoxcli

import (
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/require"
)

type shippedPolicy struct {
	Name    string
	Content string
}

var shippedPolicyEnvelopes = []string{"default", "strict", "dev", "acp", "acpx", "oracle"}

func shippedHITLPolicies(t *testing.T) []shippedPolicy {
	t.Helper()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	out := make([]shippedPolicy, 0, len(shippedPolicyEnvelopes))
	for _, name := range shippedPolicyEnvelopes {
		doc, err := agentdecl.RenderEnvelopePolicy(cfg, name, "")
		require.NoErrorf(t, err, "envelope %s renders", name)
		out = append(out, shippedPolicy{Name: agentdecl.EnvelopePolicyFile(name), Content: string(doc)})
	}
	return out
}

func shippedHITLPolicy(t *testing.T, envelope string) string {
	t.Helper()
	for _, p := range shippedHITLPolicies(t) {
		if p.Name == agentdecl.EnvelopePolicyFile(envelope) {
			return p.Content
		}
	}
	t.Fatalf("no shipped envelope %q", envelope)
	return ""
}
