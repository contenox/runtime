package acpsvc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const decidedByACPClient = "acp_client"

func (t *Transport) AskApproval(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
	contenoxSessionID, _ := ctx.Value(runtimetypes.SessionIDContextKey).(string)
	acpSessionID, ok := t.acpSessionForContenoxID(contenoxSessionID)
	if !ok {
		return false, libacp.InternalError("acpsvc: no ACP session bound to contenox session " + contenoxSessionID)
	}

	toolCallID := approvalflow.ToolCallID(req)
	rpcReq := approvalflow.BuildRequest(req, approvalflow.BuildOptions{SessionID: acpSessionID})

	t.markPermissionPending(acpSessionID, toolCallID)
	defer t.clearPermissionPending(acpSessionID, toolCallID)

	reportErr, reportChange, end := t.tracker().Start(ctx, "hitl", "acp_permission", "tool_call_id", toolCallID)
	defer end()

	askedAt := time.Now().UTC()
	resp, err := t.conn.RequestPermission(ctx, rpcReq)
	if err != nil {
		ctxErr := ""
		if e := ctx.Err(); e != nil {
			ctxErr = e.Error()
		}
		reportChange("rpc_error", err.Error())
		reportChange("ctx_err", ctxErr)
		reportErr(err)
		outcome := hitlservice.DecisionCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcome = hitlservice.DecisionTimedOut
		}
		t.recordApprovalDecision(ctx, req, contenoxSessionID, outcome, askedAt)
		return false, err
	}
	reportChange("outcome", string(resp.Outcome.Outcome))
	reportChange("option_id", resp.Outcome.OptionID)
	switch resp.Outcome.Outcome {
	case libacp.PermissionOutcomeCancelled:
		t.recordApprovalDecision(ctx, req, contenoxSessionID, hitlservice.DecisionCancelled, askedAt)
		return false, context.Canceled
	case libacp.PermissionOutcomeSelected:
		allowed := resp.Outcome.OptionID == approvalflow.OptionAllow
		outcome := hitlservice.DecisionDenied
		if allowed {
			outcome = hitlservice.DecisionApproved
		}
		t.recordApprovalDecision(ctx, req, contenoxSessionID, outcome, askedAt)
		return allowed, nil
	}
	t.recordApprovalDecision(ctx, req, contenoxSessionID, hitlservice.DecisionDenied, askedAt)
	return false, nil
}

// recordApprovalDecision writes the interactive decision to hitl_approvals
// through the durable approval service. Recording never changes the outcome of
// the ask: a failure is logged and the decision stands.
func (t *Transport) recordApprovalDecision(ctx context.Context, req hitlservice.ApprovalRequest, contenoxSessionID string, outcome hitlservice.DecisionOutcome, askedAt time.Time) {
	rec := t.deps.DecisionRecorder
	if rec == nil {
		return
	}
	if req.SessionID == "" {
		req.SessionID = contenoxSessionID
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := rec.RecordDecision(recordCtx, req, hitlservice.Decision{
		Outcome:   outcome,
		DecidedBy: decidedByACPClient,
		AskedAt:   askedAt,
	}); err != nil {
		slog.Warn("acpsvc: could not record approval decision", "tool", req.ToolName, "outcome", string(outcome), "error", err)
	}
}
