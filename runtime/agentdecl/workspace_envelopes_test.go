package agentdecl_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/project"
	"github.com/stretchr/testify/require"
)

func workspaceWith(t *testing.T, agentsToml string) (root, id string) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, project.ContenoxDirName), 0o755))
	if agentsToml != "" {
		require.NoError(t, os.WriteFile(project.LayerConfigPath(root), []byte(agentsToml), 0o644))
	}
	return root, project.LayerKey(root)
}

func TestUnit_WorkspaceEnvelopes_RenderOnlyWhatTheWorkspaceChangesAndOnlyForIt(t *testing.T) {
	home := t.TempDir()
	root, id := workspaceWith(t, `
[envelopes.review_only]
extends = "read_only"
description = "Only for this project."

[envelopes.strict]
description = "Stricter here."
`)
	names, err := agentdecl.RenderWorkspaceEnvelopes(context.Background(), home, root)
	require.NoError(t, err)
	require.Equal(t, []string{"review_only", "strict"}, names, "an envelope the project did not touch is not copied")

	dir := agentdecl.WorkspacePolicyDir(home, id)
	for _, file := range []string{"hitl-policy-review_only.json", "hitl-policy-strict.json"} {
		_, statErr := os.Stat(filepath.Join(dir, file))
		require.NoErrorf(t, statErr, "%s rendered for the workspace", file)
	}
	_, statErr := os.Stat(filepath.Join(dir, "hitl-policy-default.json"))
	require.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(filepath.Join(home, agentdecl.GeneratedDirName, "hitl-policy-review_only.json"))
	require.True(t, os.IsNotExist(statErr), "the shared folder never sees a workspace's envelope")
}

func TestUnit_WorkspaceEnvelopes_FollowTheFileAndLeaveWhenItGoes(t *testing.T) {
	home := t.TempDir()
	root, id := workspaceWith(t, "[envelopes.one]\nextends = \"read_only\"\n[envelopes.two]\nextends = \"read_only\"\n")
	ctx := context.Background()
	names, err := agentdecl.RenderWorkspaceEnvelopes(ctx, home, root)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, names)

	cfgFile := filepath.Join(root, project.ContenoxDirName, agentdecl.ConfigFilename)
	require.NoError(t, os.WriteFile(cfgFile, []byte("[envelopes.one]\nextends = \"read_only\"\n"), 0o644))
	names, err = agentdecl.RenderWorkspaceEnvelopes(ctx, home, root)
	require.NoError(t, err)
	require.Equal(t, []string{"one"}, names)
	_, statErr := os.Stat(filepath.Join(agentdecl.WorkspacePolicyDir(home, id), "hitl-policy-two.json"))
	require.True(t, os.IsNotExist(statErr), "a removed envelope's file is removed")

	require.NoError(t, os.WriteFile(cfgFile, []byte("[envelopes.one\nbroken"), 0o644))
	_, err = agentdecl.RenderWorkspaceEnvelopes(ctx, home, root)
	require.Error(t, err)
	_, statErr = os.Stat(filepath.Join(agentdecl.WorkspacePolicyDir(home, id), "hitl-policy-one.json"))
	require.NoError(t, statErr, "a broken file keeps what was last good")

	require.NoError(t, os.Remove(cfgFile))
	names, err = agentdecl.RenderWorkspaceEnvelopes(ctx, home, root)
	require.NoError(t, err)
	require.Empty(t, names)
	_, statErr = os.Stat(agentdecl.WorkspacePolicyDir(home, id))
	require.True(t, os.IsNotExist(statErr), "no project file, no workspace folder")
}

func TestUnit_WorkspaceEnvelopes_AFolderWithItsOwnFileNeedsNoRegistrationAndIsNotMarked(t *testing.T) {
	root, id := workspaceWith(t, "[envelopes.x]\nextends = \"read_only\"\n")
	home := t.TempDir()
	names, err := agentdecl.RenderWorkspaceEnvelopes(context.Background(), home, root)
	require.NoError(t, err)
	require.Equal(t, []string{"x"}, names)
	require.Equal(t, id, project.LayerKeyOf(filepath.Join(root, "deep", "er")), "any folder inside it finds the same layer")
	_, statErr := os.Stat(filepath.Join(agentdecl.WorkspacePolicyDir(home, id), "hitl-policy-x.json"))
	require.NoError(t, statErr)
	require.False(t, project.HasMarker(root), "nothing is written into the folder, so its workspace id and history stay as they were")
}

func TestUnit_WorkspaceEnvelopes_AFolderWithNothingToApplyIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	names, err := agentdecl.RenderWorkspaceEnvelopes(context.Background(), t.TempDir(), root)
	require.NoError(t, err)
	require.Empty(t, names)
	require.Empty(t, project.LayerKeyOf(root), "no agents.toml, no layer")
	require.False(t, project.HasMarker(root))
}

func TestUnit_Bootstrap_RemovesTheRenderedFileOfADeletedEnvelopeButNeverAnAgentsPolicy(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(home, agentdecl.ConfigFilename), []byte("[envelopes.temp]\nextends = \"read_only\"\n"), 0o644))
	withTemp, err := agentdecl.ConfigFromTexts("[envelopes.temp]\nextends = \"read_only\"\n")
	require.NoError(t, err)
	_, _, err = agentdecl.BootstrapFor(ctx, home, "", withTemp)
	require.NoError(t, err)
	generated := filepath.Join(home, agentdecl.GeneratedDirName)
	_, statErr := os.Stat(filepath.Join(generated, "hitl-policy-temp.json"))
	require.NoError(t, statErr, "the envelope is rendered")
	require.NoError(t, os.WriteFile(filepath.Join(generated, "hitl-policy-chat.json"), []byte(`{"version": 1, "rules": []}`), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(home, agentdecl.ConfigFilename), []byte(""), 0o644))
	_, _, err = agentdecl.BootstrapFor(ctx, home, "", cfg)
	require.NoError(t, err)
	_, statErr = os.Stat(filepath.Join(generated, "hitl-policy-temp.json"))
	require.True(t, os.IsNotExist(statErr), "an envelope that is gone from agents.toml no longer has a policy file")
	_, statErr = os.Stat(filepath.Join(generated, "hitl-policy-strict.json"))
	require.NoError(t, statErr, "the others stay")
	_, statErr = os.Stat(filepath.Join(generated, "hitl-policy-chat.json"))
	require.NoError(t, statErr, "an agent's own policy file is not an envelope's")
}
