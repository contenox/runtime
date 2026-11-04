package approvalflow_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/require"
)

type call struct {
	tools, tool string
	args        map[string]any
	policy      string
}

type fakeEvaluator struct {
	calls  []call
	decide func(c call) hitlservice.Action
}

func (f *fakeEvaluator) Evaluate(ctx context.Context, toolsName, toolName string, args map[string]any) (hitlservice.EvaluationResult, error) {
	c := call{tools: toolsName, tool: toolName, args: args, policy: hitlservice.PolicyNameFromContext(ctx)}
	f.calls = append(f.calls, c)
	return hitlservice.EvaluationResult{Action: f.decide(c), PolicyName: c.policy}, nil
}

func permissionRequest(kind libacp.ToolKind, raw string, locations ...string) libacp.RequestPermissionRequest {
	tc := libacp.PermissionToolCall{ToolCallID: "tc-1", Title: "t", Kind: kind}
	if raw != "" {
		tc.RawInput = json.RawMessage(raw)
	}
	for _, p := range locations {
		tc.Locations = append(tc.Locations, libacp.ToolCallLocation{Path: p})
	}
	return libacp.RequestPermissionRequest{
		SessionID: "s",
		ToolCall:  tc,
		Options: []libacp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: libacp.PermissionAllowOnce},
			{OptionID: "reject", Name: "Reject", Kind: libacp.PermissionRejectOnce},
		},
	}
}

func TestUnit_MapACPRequest_ClaudeShapes(t *testing.T) {
	shell := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindExecute, `{"command":"git status","description":"x"}`))
	require.Len(t, shell, 1)
	require.Equal(t, "local_shell", shell[0].ToolsName)
	require.Equal(t, "git status", shell[0].Args["command"])
	require.True(t, shell[0].ArgsKnown)

	native := filepath.Join(string(filepath.Separator)+"repo", "a.go")
	raw, err := json.Marshal(map[string]string{"file_path": native})
	require.NoError(t, err)
	edit := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindEdit, string(raw), native))
	require.Len(t, edit, 1, "the location and file_path name the same file")
	require.Equal(t, "local_fs", edit[0].ToolsName)
	require.Equal(t, "write_file", edit[0].ToolName)
	require.Equal(t, "/repo/a.go", edit[0].Args["path"], "paths are matched slash-style like the envelope globs")

	move := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindMove, "", "/w/a", "/w/b"))
	require.Len(t, move, 2)

	read := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindRead, `{"file_path":"/w/x"}`))
	require.Equal(t, "read_file", read[0].ToolName)

	fetch := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindFetch, `{"url":"https://example.com"}`))
	require.Equal(t, "native-web", fetch[0].ToolsName)
	require.Equal(t, "https://example.com", fetch[0].Args["url"])

	other := approvalflow.MapACPRequest(permissionRequest(libacp.ToolKindOther, `{"x":1}`))
	require.False(t, other[0].Named)
}

func TestUnit_Judge_AllowsDeniesAndEscalates(t *testing.T) {
	ctx := context.Background()
	eval := &fakeEvaluator{decide: func(c call) hitlservice.Action {
		if c.tools == "local_shell" {
			return hitlservice.ActionAllow
		}
		if p, _ := c.args["path"].(string); p == "/w/.ssh/id" {
			return hitlservice.ActionDeny
		}
		return hitlservice.ActionApprove
	}}

	verdict, _, escalate := approvalflow.Judge(ctx, eval, "auto_edit", permissionRequest(libacp.ToolKindExecute, `{"command":"ls"}`))
	require.Equal(t, hitlservice.ActionAllow, verdict.Action)
	require.Empty(t, escalate)
	require.Equal(t, "auto_edit", eval.calls[0].policy, "the session's policy is the one evaluated")

	verdict, mapped, escalate := approvalflow.Judge(ctx, eval, "", permissionRequest(libacp.ToolKindMove, "", "/w/a", "/w/.ssh/id"))
	require.Equal(t, hitlservice.ActionDeny, verdict.Action, "the strictest verdict across the paths wins")
	require.Empty(t, escalate)
	require.Equal(t, "/w/.ssh/id", mapped.Args["path"])

	_, _, escalate = approvalflow.Judge(ctx, eval, "", permissionRequest(libacp.ToolKindEdit, "", "/w/a"))
	require.Equal(t, approvalflow.EscalateNeedsApproval, escalate)

	_, _, escalate = approvalflow.Judge(ctx, eval, "", permissionRequest(libacp.ToolKindExecute, `{}`))
	require.Equal(t, approvalflow.EscalateAllowNoArgs, escalate, "an allow without the command still asks")

	_, _, escalate = approvalflow.Judge(ctx, eval, "", permissionRequest(libacp.ToolKindOther, ""))
	require.Equal(t, approvalflow.EscalateUnmapped, escalate)

	_, _, escalate = approvalflow.Judge(ctx, nil, "", permissionRequest(libacp.ToolKindExecute, `{"command":"ls"}`))
	require.Equal(t, approvalflow.EscalateUnmapped, escalate, "without a policy evaluator every request asks")
}

func TestUnit_WithoutStandingOptions_KeepsOnlyOneOffChoices(t *testing.T) {
	req := permissionRequest(libacp.ToolKindEdit, "")
	req.Options = []libacp.PermissionOption{
		{OptionID: "allow", Kind: libacp.PermissionAllowOnce},
		{OptionID: "allow_session", Kind: libacp.PermissionAllowAlways},
		{OptionID: "reject", Kind: libacp.PermissionRejectOnce},
	}
	got := approvalflow.WithoutStandingOptions(req).Options
	require.Len(t, got, 2)
	require.Equal(t, "allow", got[0].OptionID)
	require.Equal(t, "reject", got[1].OptionID)

	only := permissionRequest(libacp.ToolKindEdit, "")
	only.Options = []libacp.PermissionOption{{OptionID: "always", Kind: libacp.PermissionAllowAlways}}
	require.Len(t, approvalflow.WithoutStandingOptions(only).Options, 1, "an agent that offers only a standing choice keeps it")
}
