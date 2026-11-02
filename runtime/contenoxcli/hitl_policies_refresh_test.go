package contenoxcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_RetireShippedHITLPolicies_RemovesUntouchedCopiesAndKeepsEdits(t *testing.T) {
	dir := t.TempDir()
	shipped, err := os.ReadFile(filepath.Join("testdata", "retired-hitl-policy-oracle.json"))
	require.NoError(t, err)
	oracle := filepath.Join(dir, "hitl-policy-oracle.json")
	strict := filepath.Join(dir, "hitl-policy-strict.json")
	mine := filepath.Join(dir, "hitl-policy-mine.json")
	require.NoError(t, os.WriteFile(oracle, shipped, 0o600))
	require.NoError(t, os.WriteFile(strict, []byte(`{"default_action":"deny"}`), 0o600))
	require.NoError(t, os.WriteFile(mine, []byte(`{"default_action":"approve"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, retiredHITLPolicyStateFilename), []byte(`{}`), 0o600))

	removed, kept, err := retireShippedHITLPolicies(dir)
	require.NoError(t, err)
	require.Equal(t, []string{oracle}, removed, "an untouched shipped copy gives way to its envelope")
	require.Equal(t, []string{strict}, kept, "an edited copy is the operator's, so it is kept and reported")
	for _, path := range []string{strict, mine, filepath.Join(dir, retiredHITLPolicyStateFilename)} {
		_, statErr := os.Stat(path)
		require.NoErrorf(t, statErr, "%s stays", path)
	}
}

func TestUnit_RetireShippedHITLPolicies_DropsTheStateFileOnceNothingIsLeft(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, retiredHITLPolicyStateFilename)
	require.NoError(t, os.WriteFile(state, []byte(`{}`), 0o600))

	removed, kept, err := retireShippedHITLPolicies(dir)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.Empty(t, kept)
	_, statErr := os.Stat(state)
	require.True(t, os.IsNotExist(statErr))
}

func TestUnit_KnownPolicyNames_ListsEveryEnvelopeWithDefaultFirst(t *testing.T) {
	names := knownPolicyNames(t.TempDir())
	require.NotEmpty(t, names)
	require.Equal(t, "hitl-policy-default.json", names[0])
	for _, want := range []string{"hitl-policy-strict.json", "hitl-policy-dev.json", "hitl-policy-acp.json", "hitl-policy-acpx.json", "hitl-policy-oracle.json", "hitl-policy-read_only.json"} {
		require.Contains(t, names, want)
	}
}
