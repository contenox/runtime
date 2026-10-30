package hitlservice_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/require"
)

func TestUnit_FSPolicySource_AcceptsEnvelopeNamesAndRenderedFiles(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":"1","rules":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hitl-policy-strict.json"), body, 0o644))
	source := hitlservice.NewFSPolicySource(dir)

	for _, name := range []string{"strict", "hitl-policy-strict", "hitl-policy-strict.json"} {
		got, err := source.ReadPolicy(context.Background(), "", name)
		require.NoError(t, err, "name %q must resolve to the rendered envelope", name)
		require.Equal(t, body, got)
	}

	_, err := source.ReadPolicy(context.Background(), "", "no-such-envelope")
	require.Error(t, err)
}
