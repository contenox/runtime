package fleetservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/taskengine"
)

type UnattendedPermissionDeps struct {
	HITL hitlservice.Service

	Missions missionservice.Service

	Sink taskengine.TaskEventSink

	DefaultPolicyName string

	Tracker libtracker.ActivityTracker
}

func NewUnattendedPermissionAnswerer(deps UnattendedPermissionDeps) agentinstance.PermissionFallback {
	if deps.Tracker == nil {
		deps.Tracker = libtracker.NoopTracker{}
	}
	a := &unattendedAnswerer{deps: deps, calls: newMissionCallCounter(0)}
	return a.answer
}

type unattendedAnswerer struct {
	deps  UnattendedPermissionDeps
	calls *missionCallCounter
}

func (a *unattendedAnswerer) answer(ctx context.Context, req agentinstance.UnattendedPermission) (libacp.RequestPermissionResponse, error) {
	reportErr, reportChange, end := a.deps.Tracker.Start(ctx, "hitl", "unattended_permission",
		"instance_id", req.InstanceID, "session_id", string(req.SessionID), "agent_name", req.AgentName)
	defer end()

	if a.deps.HITL == nil || a.deps.Missions == nil || a.deps.Sink == nil {
		err := fmt.Errorf("fleetservice: unattended permission answerer is not fully wired")
		reportErr(err)
		return approvalflow.Answer(req.Request, false), nil
	}

	mapped := approvalflow.MapRequest(req.Request)
	policyName, missionID := a.envelope(ctx, req.InstanceID)
	reportChange("policy", policyName)
	if missionID != "" {
		reportChange("mission_id", missionID)
	}

	if reason, refuse := a.toolCallBudgetRefusal(ctx, policyName, missionID); refuse {
		reportChange("compute_bound", reason)
		if _, err := a.deps.Missions.Finish(ctx, missionID, missionservice.StatusStuck, reason); err != nil {
			reportChange("compute_bound_finish_error", err.Error())
		}
		return approvalflow.Answer(req.Request, false), nil
	}

	verdict, escalate := a.judge(ctx, policyName, mapped)
	reportChange("action", string(verdict.Action))
	if escalate != "" {
		reportChange("escalated", escalate)
	}

	if escalate == "" {
		switch verdict.Action {
		case hitlservice.ActionAllow:
			return approvalflow.Answer(req.Request, true), nil
		case hitlservice.ActionDeny:
			return approvalflow.Answer(req.Request, false), nil
		}
	}

	approved, err := a.escalate(ctx, req, mapped, verdict, policyName, missionID)
	if err != nil {
		reportErr(err)
		return approvalflow.Answer(req.Request, false), nil
	}
	reportChange("approved", approved)
	return approvalflow.Answer(req.Request, approved), nil
}

func (a *unattendedAnswerer) envelope(ctx context.Context, instanceID string) (policyName, missionID string) {
	m, err := a.deps.Missions.GetByInstance(ctx, instanceID)
	if err != nil {
		return a.deps.DefaultPolicyName, ""
	}
	if m.HITLPolicyName == "" {
		return a.deps.DefaultPolicyName, m.ID
	}
	return m.HITLPolicyName, m.ID
}

func (a *unattendedAnswerer) toolCallBudgetRefusal(ctx context.Context, policyName, missionID string) (string, bool) {
	if missionID == "" {
		return "", false
	}
	reader, ok := a.deps.HITL.(hitlservice.ComputeBoundsReader)
	if !ok {
		return "", false
	}
	bounds, err := reader.ComputeBoundsFor(ctx, policyName)
	if err != nil || bounds.MaxToolCalls <= 0 {
		return "", false
	}
	count := a.calls.increment(missionID)
	if !toolCallBudgetExceeded(count, bounds) {
		return "", false
	}
	return toolCallsExhaustedReason(bounds.MaxToolCalls), true
}

func (a *unattendedAnswerer) judge(ctx context.Context, policyName string, mapped approvalflow.Mapped) (hitlservice.EvaluationResult, string) {
	if !mapped.Named {
		return hitlservice.EvaluationResult{
			Action:     hitlservice.ActionApprove,
			Reason:     "unmapped_request",
			PolicyName: policyName,
		}, "unmapped_request"
	}

	evalCtx := ctx
	if policyName != "" {
		evalCtx = hitlservice.WithPolicyName(ctx, policyName)
	}
	verdict, err := a.deps.HITL.Evaluate(evalCtx, mapped.ToolsName, mapped.ToolName, mapped.Args)
	if err != nil {
		return hitlservice.EvaluationResult{
			Action:     hitlservice.ActionApprove,
			Reason:     "policy_error",
			PolicyName: policyName,
		}, "policy_error"
	}
	if verdict.Action == hitlservice.ActionAllow && !mapped.ArgsKnown {
		return verdict, "allow_without_args"
	}
	if verdict.Action == hitlservice.ActionApprove {
		return verdict, "policy_requires_approval"
	}
	return verdict, ""
}

func (a *unattendedAnswerer) escalate(
	ctx context.Context,
	req agentinstance.UnattendedPermission,
	mapped approvalflow.Mapped,
	verdict hitlservice.EvaluationResult,
	policyName, missionID string,
) (bool, error) {
	policy := verdict.PolicyName
	if policy == "" {
		policy = policyName
	}
	ask := hitlservice.ApprovalRequest{
		ToolCallID:  mapped.ToolCallID,
		ToolsName:   clampColumn(nonEmpty(mapped.ToolsName, unmappedToolsName)),
		ToolName:    clampColumn(nonEmpty(mapped.ToolName, mapped.Title)),
		Args:        mapped.Args,
		Diff:        mapped.Diff,
		PolicyName:  policy,
		MatchedRule: verdict.MatchedRule,
		TimeoutS:    verdict.TimeoutS,
		OnTimeout:   verdict.OnTimeout,
		InstanceID:  req.InstanceID,
		SessionID:   string(req.SessionID),
		AgentName:   req.AgentName,
		MissionID:   missionID,
	}
	askCtx := ctx
	if verdict.TimeoutS > 0 {
		var cancel context.CancelFunc
		askCtx, cancel = context.WithTimeout(ctx, time.Duration(verdict.TimeoutS)*time.Second)
		defer cancel()
	}

	approved, err := a.deps.HITL.RequestApproval(askCtx, ask, a.deps.Sink)
	if err != nil {
		if verdict.TimeoutS > 0 && errors.Is(askCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return verdict.OnTimeout == hitlservice.ActionAllow, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, fmt.Errorf("fleetservice: durable approval for unattended permission: %w", err)
	}
	return approved, nil
}

const unmappedToolsName = "acp"

func nonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

const maxToolColumn = 255

func clampColumn(v string) string {
	r := []rune(v)
	if len(r) <= maxToolColumn {
		return v
	}
	return string(r[:maxToolColumn-3]) + "..."
}
