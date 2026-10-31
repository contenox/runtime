package acpsvc

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_LoadChainRegistryFrom_MissingPathMentionsEnvVar(t *testing.T) {
	const envVar = "CONTENOX_TEST_ACP_CHAIN_PATH"
	missing := filepath.Join(t.TempDir(), "absent-chain.json")
	t.Setenv(envVar, missing)

	_, err := LoadChainRegistryFrom("unused.json", envVar)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envVar)
	assert.Contains(t, err.Error(), "absent-chain.json")
}
