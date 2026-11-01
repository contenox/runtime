package localtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
)

var approvalPending atomic.Int32

func IsApprovalPending() bool {
	return approvalPending.Load() > 0
}

type AskApproval func(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error)

// ReadPathsFunc names the files a call would print, so a single call that reads
// many files (a diff, a commit, a blame) can be checked against the read policy
// file by file, exactly as a local_fs read is. It returns no paths for a tool
// that prints names only.
type ReadPathsFunc func(ctx context.Context, toolsCall *taskengine.ToolsCall, args map[string]any) ([]string, error)

// ReadPathProvider is implemented by a tool that can list the files a call is
// about to print. The engine wires it into the HITL wrapper so a multi-file
// read cannot bypass the read rules that guard each of those files.
type ReadPathProvider interface {
	ReadPaths(ctx context.Context, toolsCall *taskengine.ToolsCall, args map[string]any) ([]string, error)
}

type HITLWrapper struct {
	inner     taskengine.ToolsRepo
	ask       AskApproval
	policy    hitlservice.PolicyEvaluator
	tracker   libtracker.ActivityTracker
	eventSink taskengine.TaskEventSink
	readPaths ReadPathsFunc
}

func NewHITLWrapper(inner taskengine.ToolsRepo, ask AskApproval, policy hitlservice.PolicyEvaluator, tracker libtracker.ActivityTracker, eventSinks ...taskengine.TaskEventSink) *HITLWrapper {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	var eventSink taskengine.TaskEventSink
	if len(eventSinks) > 0 {
		eventSink = eventSinks[0]
	}
	return &HITLWrapper{
		inner:     inner,
		ask:       ask,
		policy:    policy,
		tracker:   tracker,
		eventSink: eventSink,
	}
}

// WithReadPaths wires the multi-file read gate. Call it once with a function
// that resolves the toolset that will run; without it, read gating is off.
func (h *HITLWrapper) WithReadPaths(fn ReadPathsFunc) *HITLWrapper {
	h.readPaths = fn
	return h
}

const (
	denyMessageUser   = "The user denied the operation. Ask for clarification or try a different, less destructive approach."
	DenyMessage       = denyMessageUser
	denyMessagePolicy = "A permission policy denied this operation. Do not retry it unchanged; choose a different approach or ask the user to change the permission setting."
)

func policyDenyMessage(result hitlservice.EvaluationResult) string {
	message := denyMessagePolicy
	if result.Reason != "" && result.Reason != "deny" {
		message += " Reason: " + result.Reason + "."
	}
	if result.PolicyName != "" {
		message += " Policy: " + result.PolicyName + "."
	}
	if result.MatchedRule != nil {
		message += " Rule: " + strconv.Itoa(*result.MatchedRule) + "."
	}
	return message
}

func (h *HITLWrapper) Exec(
	ctx context.Context,
	startTime time.Time,
	input any,
	debug bool,
	tools *taskengine.ToolsCall,
) (any, taskengine.DataType, error) {
	toolName := tools.ToolName
	if toolName == "" {
		toolName = tools.Name
	}
	reportErr, reportChange, end := h.tracker.Start(ctx, "hitl", "exec", "tool_name", toolName, "args", input)
	defer end()

	if debug {
		reportChange("input", input)
	}

	args, ok := input.(map[string]any)
	if !ok {
		reportErr(fmt.Errorf("hitl: non-map input %T; When-conditions will not be evaluated", input))
		args = make(map[string]any)
	}

	result, err := h.policy.Evaluate(ctx, tools.Name, toolName, args)
	if err != nil {
		reportErr(fmt.Errorf("hitl: policy evaluation failed, denying: %w", err))
		h.publishDecision(ctx, tools.Name, toolName, args, hitlservice.EvaluationResult{
			Action: hitlservice.ActionDeny,
			Reason: "policy_error",
		}, false)
		reportChange("denied", "policy_error")
		return "", taskengine.DataTypeAny, errors.New("the permission policy could not be evaluated, so the operation was denied. This is a configuration problem, not your mistake: report the error and do not retry")
	}

	// A tool that prints several files at once (git_diff, git_show, git_blame)
	// must not bypass the read rules that guard each of those files for local_fs.
	result, readPaths := h.gateReadPaths(ctx, tools, args, result)

	switch result.Action {
	case hitlservice.ActionAllow:
		h.publishDecision(ctx, tools.Name, toolName, args, result, false)
		return h.inner.Exec(ctx, startTime, input, debug, tools)

	case hitlservice.ActionDeny:
		h.publishDecision(ctx, tools.Name, toolName, args, result, false)
		reportChange("denied", "policy")
		message := policyDenyMessage(result)
		if (result.Reason == "read_path_denied" || result.Reason == "policy_error") && result.Detail != "" {
			message += " " + result.Detail + "."
		}
		return "", taskengine.DataTypeAny, errors.New(message)

	case hitlservice.ActionApprove:
		oldContent, newContent, diffErr := h.buildDiff(ctx, tools, toolName, args)
		if diffErr != nil {
			reportErr(fmt.Errorf("hitl: diff generation failed: %w", diffErr))
		}
		rendered := ""
		if oldContent != newContent {
			filePath, _ := args["path"].(string)
			rendered = unifiedDiff(filePath, oldContent, newContent)
		}
		toolCallID, _ := ctx.Value(taskengine.ContextKeyToolCallID).(string)
		req := hitlservice.ApprovalRequest{
			ToolCallID:  toolCallID,
			ToolsName:   tools.Name,
			ToolName:    toolName,
			Args:        args,
			Diff:        rendered,
			DiffOld:     oldContent,
			DiffNew:     newContent,
			PolicyName:  result.PolicyName,
			MatchedRule: result.MatchedRule,
			Detail:      result.Detail,
			ReadPaths:   readPaths,
			TimeoutS:    result.TimeoutS,
			OnTimeout:   result.OnTimeout,
		}
		h.publishDecision(ctx, tools.Name, toolName, args, result, true)

		askCtx := ctx
		var askCancel context.CancelFunc
		if result.TimeoutS > 0 {
			askCtx, askCancel = context.WithTimeout(ctx, time.Duration(result.TimeoutS)*time.Second)
			defer askCancel()
		}

		approvalPending.Add(1)
		approved, err := h.ask(askCtx, req)
		approvalPending.Add(-1)
		if err != nil {
			if result.TimeoutS > 0 &&
				errors.Is(askCtx.Err(), context.DeadlineExceeded) &&
				ctx.Err() == nil {
				onTimeout := result.OnTimeout
				if onTimeout == "" {
					onTimeout = hitlservice.ActionDeny
				}
				if onTimeout == hitlservice.ActionAllow {
					reportChange("approved", "timeout_allow")
					return h.inner.Exec(ctx, startTime, input, debug, tools)
				}
				reportErr(fmt.Errorf("hitl: approval timed out: %w", err))
				reportChange("denied", "timeout")
				return "", taskengine.DataTypeAny, fmt.Errorf("approval timed out after %ds and the operation was automatically denied (policy %s, on_timeout=%s); ask the user before retrying",
					result.TimeoutS, policyLabel(result), onTimeout)
			}
			err = fmt.Errorf("hitl: approval error: %w", err)
			reportErr(err)
			return nil, taskengine.DataTypeAny, err
		}
		if !approved {
			reportChange("denied", "user")
			return "", taskengine.DataTypeAny, errors.New(denyMessageUser)
		}
		reportChange("approved", "user")
		return h.inner.Exec(ctx, startTime, input, debug, tools)

	default:
		h.publishDecision(ctx, tools.Name, toolName, args, result, false)
		return h.inner.Exec(ctx, startTime, input, debug, tools)
	}
}

// gateReadPaths runs the read policy over the files a multi-file read would
// print and folds the verdicts into the call's own. A denied file denies the
// whole call; a file that needs approval turns an allow into an approval.
func (h *HITLWrapper) gateReadPaths(ctx context.Context, tools *taskengine.ToolsCall, args map[string]any, top hitlservice.EvaluationResult) (hitlservice.EvaluationResult, []string) {
	if h.readPaths == nil || top.Action == hitlservice.ActionDeny {
		return top, nil
	}
	paths, err := h.readPaths(ctx, tools, args)
	if err != nil {
		// The tool will hit the same repository problem before it can print
		// anything; let its own error explain that instead of a policy denial.
		return top, nil
	}
	var approvals []string
	for _, p := range paths {
		r, evalErr := h.policy.Evaluate(ctx, LocalFSToolsName, "read_file", map[string]any{"path": p})
		if evalErr != nil {
			return hitlservice.EvaluationResult{
				Action: hitlservice.ActionDeny,
				Reason: "policy_error",
				Detail: "the read policy could not be evaluated for " + p + ": " + evalErr.Error(),
			}, nil
		}
		switch r.Action {
		case hitlservice.ActionDeny:
			return hitlservice.EvaluationResult{
				Action:      hitlservice.ActionDeny,
				Reason:      "read_path_denied",
				Detail:      "this call would read " + p + ", which the policy denies",
				PolicyName:  r.PolicyName,
				MatchedRule: r.MatchedRule,
			}, nil
		case hitlservice.ActionApprove:
			approvals = append(approvals, p)
			if top.Action == hitlservice.ActionAllow {
				top.Action = hitlservice.ActionApprove
				top.Reason = "read_path_approval"
				top.PolicyName = r.PolicyName
				top.MatchedRule = r.MatchedRule
				top.TimeoutS = r.TimeoutS
				top.OnTimeout = r.OnTimeout
			}
		}
	}
	if len(approvals) > 0 {
		note := "this call would read " + strings.Join(approvals, ", ")
		if top.Detail != "" {
			top.Detail += "; " + note
		} else {
			top.Detail = note
		}
	}
	return top, paths
}

func policyLabel(result hitlservice.EvaluationResult) string {
	if strings.TrimSpace(result.PolicyName) != "" {
		return result.PolicyName
	}
	return "unnamed policy"
}

func (h *HITLWrapper) publishDecision(ctx context.Context, toolsName, toolName string, args map[string]any, result hitlservice.EvaluationResult, approvalRequested bool) {
	if h.eventSink == nil || !h.eventSink.Wants(taskengine.TaskEventHITLDecision) {
		return
	}
	ev := taskengine.NewTaskEvent(ctx, taskengine.TaskEventHITLDecision)
	ev.HookName = toolsName
	ev.ToolName = toolName
	ev.HITLAction = string(result.Action)
	ev.HITLReason = result.Reason
	ev.HITLPolicyName = result.PolicyName
	ev.HITLArgsSummary = hitlArgsSummary(args)
	ev.HITLMatchedRule = result.MatchedRule
	ev.HITLTimeoutS = result.TimeoutS
	ev.HITLApprovalRequested = boolPtr(approvalRequested)
	if err := h.eventSink.PublishTaskEvent(ctx, ev); err != nil {
		reportErr, _, end := h.tracker.Start(ctx, "publish", "hitl_decision", "tool_name", toolName)
		reportErr(err)
		end()
	}
}

func hitlArgsSummary(args map[string]any) string {
	for _, key := range []string{"path", "command", "url", "pattern"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return trimHITLSummary(v)
		}
	}
	return ""
}

func trimHITLSummary(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len([]rune(s)) > 96 {
		return string([]rune(s)[:95]) + "..."
	}
	return s
}

func boolPtr(v bool) *bool {
	return &v
}

func (h *HITLWrapper) Supports(ctx context.Context) ([]string, error) {
	return h.inner.Supports(ctx)
}

func (h *HITLWrapper) GetSchemasForSupportedTools(ctx context.Context) (map[string]*openapi3.T, error) {
	return h.inner.GetSchemasForSupportedTools(ctx)
}

func (h *HITLWrapper) GetToolsForToolsByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	return h.inner.GetToolsForToolsByName(ctx, name)
}

var _ taskengine.ToolsRepo = (*HITLWrapper)(nil)

func (h *HITLWrapper) buildDiff(ctx context.Context, tools *taskengine.ToolsCall, toolName string, args map[string]any) (string, string, error) {
	switch {
	case tools.Name == "local_fs" && toolName == "write_file":
		path, _ := args["path"].(string)
		newContent, _ := args["content"].(string)
		if path == "" {
			return "", "", nil
		}
		oldContent, err := h.readViaTools(ctx, tools, path)
		if err != nil {
			return "", "", err
		}
		return oldContent, newContent, nil

	case tools.Name == "local_fs" && toolName == "sed":
		path, _ := args["path"].(string)
		pattern, _ := args["pattern"].(string)
		replacement, _ := args["replacement"].(string)
		if path == "" || pattern == "" {
			return "", "", nil
		}
		oldContent, err := h.readViaTools(ctx, tools, path)
		if err != nil {
			return "", "", err
		}
		newContent := strings.ReplaceAll(oldContent, pattern, replacement)
		return oldContent, newContent, nil
	}
	return "", "", nil
}

func (h *HITLWrapper) readViaTools(ctx context.Context, tools *taskengine.ToolsCall, path string) (string, error) {
	readCall := &taskengine.ToolsCall{Name: tools.Name, ToolName: "read_file"}
	result, _, err := h.inner.Exec(ctx, time.Now(), map[string]any{"path": path}, false, readCall)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	s, _ := result.(string)
	return s, nil
}

const (
	diffMaxFileLines   = 500
	diffContext        = 3
	diffMaxOutputLines = 120
)

type editOp struct {
	kind byte
	text string
}

func lcsEditScript(old, new []string) []editOp {
	m, n := len(old), len(new)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	ops := make([]editOp, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && old[i-1] == new[j-1]:
			ops = append(ops, editOp{' ', old[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, editOp{'+', new[j-1]})
			j--
		default:
			ops = append(ops, editOp{'-', old[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func unifiedDiff(filename, oldStr, newStr string) string {
	if oldStr == newStr {
		return "(no changes)"
	}

	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)

	truncated := false
	if len(oldLines) > diffMaxFileLines {
		oldLines = oldLines[:diffMaxFileLines]
		truncated = true
	}
	if len(newLines) > diffMaxFileLines {
		newLines = newLines[:diffMaxFileLines]
		truncated = true
	}

	ops := lcsEditScript(oldLines, newLines)

	include := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind != ' ' {
			lo := max(0, i-diffContext)
			hi := min(len(ops), i+diffContext+1)
			for k := lo; k < hi; k++ {
				include[k] = true
			}
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s (current)\n+++ %s (proposed)\n", filename, filename)

	outputLines := 0
	inHunk := false
	var hunkOldStart, hunkNewStart, hunkOldCount, hunkNewCount int
	var hunkBuf []string
	oldN, newN := 1, 1

	flushHunk := func() {
		if !inHunk {
			return
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", hunkOldStart, hunkOldCount, hunkNewStart, hunkNewCount)
		for _, l := range hunkBuf {
			sb.WriteString(l)
		}
		hunkBuf = hunkBuf[:0]
		inHunk = false
	}

	for i, op := range ops {
		if !include[i] {
			flushHunk()
			oldN++
			newN++
			continue
		}
		if !inHunk {
			hunkOldStart = oldN
			hunkNewStart = newN
			hunkOldCount = 0
			hunkNewCount = 0
			inHunk = true
		}
		switch op.kind {
		case ' ':
			hunkBuf = append(hunkBuf, fmt.Sprintf(" %s\n", op.text))
			hunkOldCount++
			hunkNewCount++
			oldN++
			newN++
		case '-':
			hunkBuf = append(hunkBuf, fmt.Sprintf("-%s\n", op.text))
			hunkOldCount++
			oldN++
		case '+':
			hunkBuf = append(hunkBuf, fmt.Sprintf("+%s\n", op.text))
			hunkNewCount++
			newN++
		}
		outputLines++
		if outputLines >= diffMaxOutputLines {
			flushHunk()
			sb.WriteString("... (diff truncated)\n")
			if truncated {
				fmt.Fprintf(&sb, "... (file truncated to first %d lines)\n", diffMaxFileLines)
			}
			return sb.String()
		}
	}
	flushHunk()

	if truncated {
		fmt.Fprintf(&sb, "... (file truncated to first %d lines)\n", diffMaxFileLines)
	}
	return sb.String()
}
