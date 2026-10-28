package contenoxcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/stretchr/testify/require"
)

func TestUnit_OracleChainCandidatesAcceptNamesAndFilenames(t *testing.T) {
	require.Contains(t, oracleChainCandidates("oracle"), "chain-oracle.json")
	require.Contains(t, oracleChainCandidates("default"), "chain-oracle-default.json")
	require.Equal(t, []string{"chain-oracle-default.json"}, oracleChainCandidates("chain-oracle-default.json"))
	require.Contains(t, oracleChainCandidates("reviewer"), "chain-oracle-reviewer.json")
}

func TestUnit_ReadOracleConfigUsesStoredDefaults(t *testing.T) {
	ctx, _, store := setupSQLiteStore(t)
	require.NoError(t, clikv.WriteConfig(ctx, store, "global", configKeyOracleChain, "chain-oracle-default.json"))
	require.NoError(t, clikv.WriteConfig(ctx, store, "global", configKeyOraclePolicy, "hitl-policy-oracle.json"))
	require.NoError(t, clikv.WriteConfig(ctx, store, "global", configKeyOracleApprovesCall, "true"))

	cfg := readOracleConfig(ctx, store)
	require.True(t, cfg.enabled())
	require.Equal(t, "chain-oracle-default.json", cfg.chain)
	require.Equal(t, "hitl-policy-oracle.json", cfg.policy)
	require.True(t, cfg.approves)

	require.NoError(t, clikv.WriteConfig(ctx, store, "global", configKeyOracleChain, "off"))
	require.False(t, readOracleConfig(ctx, store).enabled())
}

func TestUnit_LoadOracleChainFindsSeededChain(t *testing.T) {
	ctx, _, _ := setupSQLiteStore(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "chain-oracle-default.json"), []byte(initOracleChain), 0o644))

	chain, ref, err := loadOracleChain(ctx, dir, oracleConfig{chain: "chain-oracle-default.json", policy: "hitl-policy-oracle.json"})
	require.NoError(t, err)
	require.NotNil(t, chain)
	require.True(t, strings.HasSuffix(ref, "chain-oracle-default.json"))
}

func TestUnit_OracleMountedLineNamesTheScope(t *testing.T) {
	line := oracleMountedLine(oracleConfig{chain: "chain-oracle-default.json", policy: "hitl-policy-oracle.json"})
	require.Contains(t, line, "questions")
	require.NotContains(t, line, "AND")

	line = oracleMountedLine(oracleConfig{chain: "chain-oracle-default.json", policy: "hitl-policy-oracle.json", approves: true})
	require.Contains(t, line, "AND its approve-tier tool calls")
}
