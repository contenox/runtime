package approvalflow

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/hitlservice"
)

const (
	acpShellToolsName = "local_shell"
	acpFSToolsName    = "local_fs"
	acpWebToolsName   = "native-web"
)

const (
	EscalateUnmapped      = "unmapped_request"
	EscalatePolicyError   = "policy_error"
	EscalateAllowNoArgs   = "allow_without_args"
	EscalateNeedsApproval = "policy_requires_approval"
)

func MapACPRequest(req libacp.RequestPermissionRequest) []Mapped {
	base := MapRequest(req)
	if base.Named {
		return []Mapped{base}
	}
	tc := req.ToolCall
	raw := base.Args
	named := func(tools, tool string, args map[string]any, known bool) Mapped {
		m := base
		m.ToolsName, m.ToolName, m.Args, m.Named, m.ArgsKnown = tools, tool, args, true, known
		return m
	}
	switch tc.Kind {
	case libacp.ToolKindExecute:
		command := firstString(raw, "command", "cmd")
		return []Mapped{named(acpShellToolsName, acpShellToolsName, map[string]any{"command": command}, command != "")}
	case libacp.ToolKindRead, libacp.ToolKindSearch, libacp.ToolKindEdit, libacp.ToolKindDelete, libacp.ToolKindMove:
		tool := "read_file"
		switch tc.Kind {
		case libacp.ToolKindSearch:
			tool = "grep"
		case libacp.ToolKindEdit, libacp.ToolKindDelete, libacp.ToolKindMove:
			tool = "write_file"
		}
		paths := toolCallPaths(tc, raw)
		if len(paths) == 0 {
			return []Mapped{named(acpFSToolsName, tool, map[string]any{}, false)}
		}
		out := make([]Mapped, 0, len(paths))
		for _, p := range paths {
			out = append(out, named(acpFSToolsName, tool, map[string]any{"path": p}, true))
		}
		return out
	case libacp.ToolKindFetch:
		url := firstString(raw, "url", "uri")
		return []Mapped{named(acpWebToolsName, "web_get", map[string]any{"url": url}, url != "")}
	}
	return []Mapped{base}
}

func Judge(ctx context.Context, svc hitlservice.PolicyEvaluator, policyName string, req libacp.RequestPermissionRequest) (hitlservice.EvaluationResult, Mapped, string) {
	evalCtx := ctx
	if policyName != "" {
		evalCtx = hitlservice.WithPolicyName(ctx, policyName)
	}
	var (
		worst       hitlservice.EvaluationResult
		worstMapped Mapped
		escalate    string
		worstRank   = -1
	)
	for _, m := range MapACPRequest(req) {
		res, esc := judgeOne(evalCtx, svc, policyName, m)
		rank := 0
		switch {
		case res.Action == hitlservice.ActionDeny && esc == "":
			rank = 2
		case esc != "":
			rank = 1
		}
		if rank > worstRank {
			worst, worstMapped, escalate, worstRank = res, m, esc, rank
		}
	}
	return worst, worstMapped, escalate
}

func judgeOne(ctx context.Context, svc hitlservice.PolicyEvaluator, policyName string, m Mapped) (hitlservice.EvaluationResult, string) {
	if !m.Named || svc == nil {
		return hitlservice.EvaluationResult{Action: hitlservice.ActionApprove, Reason: EscalateUnmapped, PolicyName: policyName}, EscalateUnmapped
	}
	res, err := svc.Evaluate(ctx, m.ToolsName, m.ToolName, m.Args)
	if err != nil {
		return hitlservice.EvaluationResult{Action: hitlservice.ActionApprove, Reason: EscalatePolicyError, PolicyName: policyName}, EscalatePolicyError
	}
	switch {
	case res.Action == hitlservice.ActionAllow && !m.ArgsKnown:
		return res, EscalateAllowNoArgs
	case res.Action == hitlservice.ActionApprove:
		return res, EscalateNeedsApproval
	}
	return res, ""
}

func toolCallPaths(tc libacp.PermissionToolCall, raw map[string]any) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		p = filepath.ToSlash(p)
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, loc := range tc.Locations {
		add(loc.Path)
	}
	for _, key := range []string{"file_path", "path", "notebook_path", "source", "destination"} {
		add(firstString(raw, key))
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func WithoutStandingOptions(req libacp.RequestPermissionRequest) libacp.RequestPermissionRequest {
	has := map[libacp.PermissionOptionKind]bool{}
	for _, o := range req.Options {
		has[o.Kind] = true
	}
	kept := make([]libacp.PermissionOption, 0, len(req.Options))
	for _, o := range req.Options {
		if o.Kind == libacp.PermissionAllowAlways && has[libacp.PermissionAllowOnce] {
			continue
		}
		if o.Kind == libacp.PermissionRejectAlways && has[libacp.PermissionRejectOnce] {
			continue
		}
		kept = append(kept, o)
	}
	req.Options = kept
	return req
}
