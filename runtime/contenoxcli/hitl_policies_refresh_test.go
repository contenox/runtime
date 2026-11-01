package contenoxcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_RefreshEmbeddedHITLPolicies_CreatesMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	written, err := refreshEmbeddedHITLPolicies(dir)
	require.NoError(t, err)
	require.Len(t, written, len(HITLPolicyPresets))

	for _, preset := range HITLPolicyPresets {
		body, err := os.ReadFile(filepath.Join(dir, preset.Name))
		require.NoError(t, err)
		assert.Equal(t, preset.Content, string(body), preset.Name)
	}
	state := readHITLPolicyState(filepath.Join(dir, hitlPolicyStateFilename))
	for _, preset := range HITLPolicyPresets {
		assert.Equal(t, digestPolicy([]byte(preset.Content)), state.Hashes[preset.Name], preset.Name)
	}
}

func TestUnit_RefreshEmbeddedHITLPolicies_UpdatesUntouchedOldShippedCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const name = "hitl-policy-dev.json"
	old := []byte("{\n  \"default_action\": \"allow\",\n  \"rules\": []\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), old, 0o644))
	state := `{"hashes":{"` + name + `":"` + digestPolicy(old) + `"}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, hitlPolicyStateFilename), []byte(state), 0o644))

	written, err := refreshEmbeddedHITLPolicies(dir)
	require.NoError(t, err)
	assert.Contains(t, written, name)

	body, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	for _, preset := range HITLPolicyPresets {
		if preset.Name == name {
			assert.Equal(t, preset.Content, string(body))
		}
	}
}

func TestUnit_RefreshEmbeddedHITLPolicies_KeepsOperatorEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const name = "hitl-policy-default.json"
	edited := []byte("{\n  \"default_action\": \"deny\",\n  \"rules\": []\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), edited, 0o644))

	written, err := refreshEmbeddedHITLPolicies(dir)
	require.NoError(t, err)
	assert.NotContains(t, written, name)
	body, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	assert.Equal(t, string(edited), string(body), "an operator edit must survive a shipped-policy refresh")

	// A second run must keep it too: the recorded hash is ours, not the edit's.
	written, err = refreshEmbeddedHITLPolicies(dir)
	require.NoError(t, err)
	assert.NotContains(t, written, name)
	body, err = os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	assert.Equal(t, string(edited), string(body))
}
