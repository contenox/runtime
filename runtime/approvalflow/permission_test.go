package approvalflow

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/require"
)

func TestUnit_BuildRequest_CarriesSedInputAndDiffForApprovalCard(t *testing.T) {
	oldText := "Questions: hello@contenox.com\n"
	newText := "Questions: **hello@contenox.com**\n"
	unified := "--- README.md\n+++ README.md\n@@\n-Questions: hello@contenox.com\n+Questions: **hello@contenox.com**\n"

	req := BuildRequest(hitlservice.ApprovalRequest{
		ToolCallID: "call-1",
		ToolsName:  "local_fs",
		ToolName:   "sed",
		Args: map[string]any{
			"path":        "README.md",
			"pattern":     "Questions: hello@contenox.com",
			"replacement": "Questions: **hello@contenox.com**",
		},
		Diff:    unified,
		DiffOld: oldText,
		DiffNew: newText,
	}, BuildOptions{
		SessionID:  "session-1",
		PolicyName: "hitl-policy-strict.json",
		PolicyPath: "/home/user/.contenox/hitl-policy-strict.json",
	})

	require.Equal(t, libacp.SessionID("session-1"), req.SessionID)
	require.Equal(t, "call-1", req.ToolCall.ToolCallID)
	require.Equal(t, "local_fs.sed: Questions: hello@contenox.com in README.md", req.ToolCall.Title)
	require.Equal(t, libacp.ToolKindEdit, req.ToolCall.Kind)
	require.JSONEq(t, `{"path":"README.md","pattern":"Questions: hello@contenox.com","replacement":"Questions: **hello@contenox.com**"}`, string(req.ToolCall.RawInput))

	require.Len(t, req.ToolCall.Content, 1)
	require.Equal(t, libacp.ToolCallContentDiff, req.ToolCall.Content[0].Type)
	require.Equal(t, "README.md", req.ToolCall.Content[0].Path)
	require.Equal(t, oldText, req.ToolCall.Content[0].OldText)
	require.Equal(t, newText, req.ToolCall.Content[0].NewText)

	var meta Meta
	require.NoError(t, json.Unmarshal(req.ToolCall.Meta, &meta))
	require.Equal(t, "local_fs", meta.ToolsName)
	require.Equal(t, "sed", meta.ToolName)
	require.Equal(t, "hitl-policy-strict.json", meta.PolicyName)
	require.Equal(t, "/home/user/.contenox/hitl-policy-strict.json", meta.PolicyPath)
	require.Equal(t, unified, meta.Diff)
	require.Equal(t, oldText, meta.DiffOld)
	require.Equal(t, newText, meta.DiffNew)
}

func TestUnit_DiffContent_BlankOldAndNewYieldsNil(t *testing.T) {
	require.Nil(t, DiffContent("README.md", "", ""))
}

func TestUnit_BuildRequest_ReadPathsBecomeToolCallLocations(t *testing.T) {
	req := BuildRequest(hitlservice.ApprovalRequest{
		ToolCallID: "call-2",
		ToolsName:  "native-git",
		ToolName:   "git_diff",
		ReadPaths:  []string{".env", "src/main.go"},
	}, BuildOptions{SessionID: "session-1"})

	require.Len(t, req.ToolCall.Locations, 2)
	require.Equal(t, ".env", req.ToolCall.Locations[0].Path)
	require.Equal(t, "src/main.go", req.ToolCall.Locations[1].Path)
}

func TestUnit_ToolKindFor_NativeGit(t *testing.T) {
	t.Parallel()
	cases := map[string]libacp.ToolKind{
		"git_status":          libacp.ToolKindRead,
		"git_diff":            libacp.ToolKindRead,
		"git_log":             libacp.ToolKindRead,
		"git_show":            libacp.ToolKindRead,
		"git_branch_list":     libacp.ToolKindRead,
		"git_blame":           libacp.ToolKindRead,
		"git_add":             libacp.ToolKindEdit,
		"git_commit":          libacp.ToolKindEdit,
		"git_checkout_branch": libacp.ToolKindEdit,
		"git_restore":         libacp.ToolKindDelete,
	}
	for name, want := range cases {
		if got := ToolKindFor("native-git", name); got != want {
			t.Errorf("ToolKindFor(native-git, %q) = %q, want %q", name, got, want)
		}
	}
}
