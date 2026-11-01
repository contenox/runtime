package localtools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

const (
	RefusalOutsideProject      = "command_outside_project"
	RefusalNotAllowlisted      = "command_not_allowlisted"
	RefusalDenied              = "command_denied_by_policy"
	RefusalShellNeedsAllowlist = "shell_requires_allowlist"
	RefusalUngated             = "shell_gated_nowhere"
)

type PolicyRefusal struct {
	Code     string
	Command  string
	Resolved string
	Project  string

	LookupErr string

	AllowedCommands []string
	DeniedCommands  []string
	ToolDirs        []string

	Hint string
}

func (r *PolicyRefusal) Error() string {
	if r == nil {
		return ""
	}
	head := fmt.Sprintf("local_shell refused %q", r.Command)
	switch r.Code {
	case RefusalNotAllowlisted:
		head += ": it is not in the allowlist."
	case RefusalDenied:
		head += ": the policy denies this program."
	case RefusalOutsideProject:
		if r.Resolved != "" && !sameCommand(r.Command, r.Resolved) {
			head += fmt.Sprintf(": it resolves to %s, which is outside the project and outside every known toolchain location.", r.Resolved)
		} else {
			head += ": it is outside the project and outside every known toolchain location."
		}
	case RefusalShellNeedsAllowlist:
		head += ": 'shell: true' needs an allowlist to check the whole line against."
	case RefusalUngated:
		head += ": nothing gates this session — no HITL policy and no allowlist."
	default:
		head += "."
	}

	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "\n  reason:  %s", r.Code)
	if r.Resolved != "" && r.Code != RefusalOutsideProject {
		fmt.Fprintf(&b, "\n  resolved: %s", r.Resolved)
	}
	if r.LookupErr != "" {
		fmt.Fprintf(&b, "\n  lookup:  %s", r.LookupErr)
	}
	if r.Project != "" {
		fmt.Fprintf(&b, "\n  project: %s", r.Project)
	}
	if len(r.AllowedCommands) > 0 && r.Code != RefusalOutsideProject {
		fmt.Fprintf(&b, "\n  allowed: %s", strings.Join(r.AllowedCommands, ", "))
	}
	if len(r.DeniedCommands) > 0 {
		fmt.Fprintf(&b, "\n  denied:  %s", strings.Join(r.DeniedCommands, ", "))
	}
	if r.Hint != "" {
		fmt.Fprintf(&b, "\n  fix:     %s", r.Hint)
	}
	b.WriteString("\n  (fatal: refused by configuration — retrying the same call cannot succeed; tell the user what to grant)")
	return b.String()
}

func (r *PolicyRefusal) Telemetry() map[string]any {
	if r == nil {
		return nil
	}
	out := map[string]any{
		"code":    r.Code,
		"command": r.Command,
	}
	if r.Resolved != "" {
		out["resolved"] = r.Resolved
	}
	if r.LookupErr != "" {
		out["lookup_error"] = r.LookupErr
	}
	if r.Project != "" {
		out["project"] = r.Project
	}
	if len(r.AllowedCommands) > 0 {
		out["allowed_commands"] = r.AllowedCommands
	}
	if len(r.DeniedCommands) > 0 {
		out["denied_commands"] = r.DeniedCommands
	}
	if len(r.ToolDirs) > 0 {
		out["tool_dirs"] = r.ToolDirs
	}
	if r.Hint != "" {
		out["fix"] = r.Hint
	}
	return out
}

func sameCommand(command, resolved string) bool {
	return strings.TrimSpace(command) == strings.TrimSpace(resolved)
}

func logRefusal(ctx context.Context, r *PolicyRefusal) {
	if r == nil {
		return
	}
	slog.WarnContext(ctx, "local_shell refused command",
		"code", r.Code,
		"command", r.Command,
		"resolved", r.Resolved,
		"lookup_error", r.LookupErr,
		"project", r.Project,
		"fix", r.Hint,
	)
}

func logUnresolved(ctx context.Context, res commandResolution, direct bool) {
	if direct {
		slog.WarnContext(ctx, "local_shell could not locate command",
			"command", res.Command,
			"error", res.LookupErr,
		)
		return
	}
	slog.DebugContext(ctx, "local_shell could not locate a shell line's first word",
		"command", res.Command,
		"error", res.LookupErr,
	)
}
