package contenoxcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_DefaultChain_MissingEnvPathNamesTheVariable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-chain.json")
	t.Setenv(acpProfileACP.chainEnv, missing)

	_, err := defaultChainFor(t.TempDir(), acpProfileACP)
	require.Error(t, err)
	assert.Contains(t, err.Error(), acpProfileACP.chainEnv)
	assert.Contains(t, err.Error(), "missing-chain.json")
}

func TestUnit_DefaultChain_UsesAValidEnvPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env-chain.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"id":"env-chain","tasks":[{"id":"reply","handler":"noop"}]}`), 0o644))
	t.Setenv(acpProfileACP.chainEnv, path)

	registry, err := defaultChainFor(t.TempDir(), acpProfileACP)
	require.NoError(t, err)
	assert.Equal(t, "env-chain", registry.Default().ID)
	assert.Equal(t, path, registry.Source())
}
