package contenoxcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/project"
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

func TestUnit_PolicyCatalog_ListsEnvelopesAndHandWrittenPoliciesButNotAgentPostures(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	require.NoError(t, seedAgentDeclarations(context.Background(), dataDir))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "hitl-policy-mine.json"), []byte(`{"//": "My own rules.", "version": 1, "default_action": "approve", "rules": []}`), 0o600))

	names := policyCatalog(dataDir)("")
	require.NotEmpty(t, names)
	require.Equal(t, "hitl-policy-default.json", names[0])
	for _, want := range []string{"hitl-policy-strict.json", "hitl-policy-dev.json", "hitl-policy-acp.json", "hitl-policy-acpx.json", "hitl-policy-oracle.json", "hitl-policy-read_only.json", "hitl-policy-mine.json"} {
		require.Contains(t, names, want)
	}
	for _, posture := range []string{"hitl-policy-chat.json", "hitl-policy-run.json", "hitl-policy-reviewer.json"} {
		_, err := os.Stat(filepath.Join(dataDir, ".generated", posture))
		require.NoError(t, err, "%s is rendered for its agent", posture)
		require.NotContains(t, names, posture, "an agent's own posture policy is not a choice")
	}
}

func TestUnit_WorkspaceEnvelopes_ReachOnlyTheirWorkspaceAndEveryRootIsRead(t *testing.T) {
	isolateHome(t)
	data := t.TempDir()
	workspace := func(name string) (string, string) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".contenox"), 0o755))
		require.NoError(t, os.WriteFile(project.LayerConfigPath(root),
			[]byte("[envelopes."+name+"]\nextends = \"default\"\ndescription = \"only "+name+"\"\n"), 0o644))
		return root, project.LayerKey(root)
	}
	served, servedID := workspace("served")
	other, otherID := workspace("other")
	t.Chdir(served)

	require.NoError(t, seedAgentDeclarations(context.Background(), data))
	renderWorkspaceEnvelopes(context.Background(), data, []string{served, other})

	catalog := policyCatalog(data)
	require.NotContains(t, catalog(""), "hitl-policy-served.json", "a project's envelope never joins the shared list")
	require.NotContains(t, catalog(""), "hitl-policy-other.json")
	require.Contains(t, catalog(servedID), "hitl-policy-served.json")
	require.NotContains(t, catalog(servedID), "hitl-policy-other.json")
	require.Contains(t, catalog(otherID), "hitl-policy-other.json", "a workspace that is not the server's folder is read too")
	require.NotContains(t, catalog(otherID), "hitl-policy-served.json")
	_, err := os.Stat(filepath.Join(data, ".generated", "hitl-policy-served.json"))
	require.True(t, os.IsNotExist(err), "nothing from a project is rendered into the shared generated folder")

	resolve := policyResolver(data)
	require.Equal(t, "workspaces/"+otherID+"/hitl-policy-other.json", resolve(otherID, "other"))
	require.Equal(t, "other", resolve(servedID, "other"))
}

func TestUnit_WorkspaceEnvelopeSignature_ChangesWithTheFileAndTheRoots(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(a, ".contenox"), 0o755))
	before := workspaceEnvelopeSignature([]string{a})
	require.Equal(t, before, workspaceEnvelopeSignature([]string{a}))
	require.NotEqual(t, before, workspaceEnvelopeSignature([]string{a, b}), "a new workspace root")
	require.NoError(t, os.WriteFile(filepath.Join(a, ".contenox", "agents.toml"), []byte("[envelopes.x]\n"), 0o644))
	require.NotEqual(t, before, workspaceEnvelopeSignature([]string{a}), "a saved agents.toml")
}
