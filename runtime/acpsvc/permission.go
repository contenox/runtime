package acpsvc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

const (
	decidedByACPClient = "acp_client"
	decidedByPolicy    = "policy"
	decidedByOracle    = "oracle"

	oracleApprovedNote    = "Approved by Oracle: part of your request"
	oracleDeniedNote      = "Denied by Oracle: not part of your request"
	elsewhereApprovedNote = "Approved from the Inbox"
	elsewhereDeniedNote   = "Denied from the Inbox"
	askExpiredNote        = "No answer before the deadline"

	approvalMetaKey = "contenox.approval"
)

type approvalMeta struct {
	DecidedBy string `json:"decidedBy"`
	Approved  bool   `json:"approved"`
	Note      string `json:"note"`
}

type askOutcome struct {
	verdict hitlservice.Verdict
	err     error
}

type clientOutcome struct {
	resp libacp.RequestPermissionResponse
	err  error
}

func (t *Transport) AskApproval(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
	contenoxSessionID, _ := ctx.Value(runtimetypes.SessionIDContextKey).(string)
	acpSessionID, ok := t.acpSessionForContenoxID(contenoxSessionID)
	if !ok {
		return false, libacp.InternalError("acpsvc: no ACP session bound to contenox-runtime session " + contenoxSessionID)
	}

	toolCallID := approvalflow.ToolCallID(req)
	rpcReq := approvalflow.BuildRequest(req, approvalflow.BuildOptions{SessionID: acpSessionID})

	t.markPermissionPending(acpSessionID, toolCallID)
	defer t.clearPermissionPending(acpSessionID, toolCallID)

	reportErr, reportChange, end := t.tracker().Start(ctx, "hitl", "acp_permission", "tool_call_id", toolCallID)
	defer end()

	askedAt := time.Now().UTC()
	ask := t.openAsk(ctx, req, contenoxSessionID, acpSessionID)
	if ask == nil {
		resp, err := t.conn.RequestPermission(ctx, rpcReq)
		allowed, outcome, err := clientAnswer(ctx, resp, err, reportErr, reportChange)
		t.recordApprovalDecision(ctx, req, contenoxSessionID, outcome, askedAt)
		return allowed, err
	}
	defer ask.Close()

	decided := make(chan askOutcome, 1)
	go func() {
		verdict, err := ask.Wait(ctx)
		decided <- askOutcome{verdict: verdict, err: err}
	}()

	rpcCtx, cancelRPC := context.WithCancel(ctx)
	defer cancelRPC()
	answered := make(chan clientOutcome, 1)
	go func() {
		resp, err := t.conn.RequestPermission(rpcCtx, rpcReq)
		answered <- clientOutcome{resp: resp, err: err}
	}()

	select {
	case a := <-answered:
		allowed, outcome, err := clientAnswer(ctx, a.resp, a.err, reportErr, reportChange)
		if outcome == hitlservice.DecisionTimedOut {
			return allowed, err
		}
		reason := ""
		if outcome == hitlservice.DecisionCancelled {
			reason = string(outcome)
		}
		respondErr := t.deps.Approvals.RespondBy(ctx, ask.ID, allowed, decidedByACPClient, reason)
		if respondErr == nil {
			return allowed, err
		}
		if !errors.Is(respondErr, hitlservice.ErrApprovalAlreadyResolved) && !errors.Is(respondErr, hitlservice.ErrApprovalExpired) {
			slog.Warn("acpsvc: could not record the client's decision", "tool", req.ToolName, "error", respondErr)
			return allowed, err
		}
		d := <-decided
		return d.verdict.Approved, d.err
	case d := <-decided:
		cancelRPC()
		reportChange("outcome", "resolved_elsewhere")
		reportChange("decided_by", d.verdict.DecidedBy)
		t.withdrawCard(ctx, acpSessionID, toolCallID, d)
		return d.verdict.Approved, d.err
	}
}

func (t *Transport) openAsk(ctx context.Context, req hitlservice.ApprovalRequest, contenoxSessionID string, sid libacp.SessionID) *hitlservice.OpenAsk {
	if t.deps.Approvals == nil {
		return nil
	}
	if req.SessionID == "" {
		req.SessionID = contenoxSessionID
	}
	if sess, ok := t.sessionFor(sid); ok && t.sessionOracle(sess) && t.offersOracle(ctx, sess) {
		req.AgentReview = true
		req.Intent = sess.lastPrompt()
	}
	ask, err := t.deps.Approvals.OpenApproval(ctx, req, taskengine.NoopTaskEventSink{})
	if err != nil {
		slog.Warn("acpsvc: could not open a durable approval; asking the client directly", "tool", req.ToolName, "error", err)
		return nil
	}
	return ask
}

func clientAnswer(ctx context.Context, resp libacp.RequestPermissionResponse, err error, reportErr func(error), reportChange func(string, any)) (bool, hitlservice.DecisionOutcome, error) {
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
		return false, outcome, err
	}
	reportChange("outcome", string(resp.Outcome.Outcome))
	reportChange("option_id", resp.Outcome.OptionID)
	switch resp.Outcome.Outcome {
	case libacp.PermissionOutcomeCancelled:
		return false, hitlservice.DecisionCancelled, context.Canceled
	case libacp.PermissionOutcomeSelected:
		if resp.Outcome.OptionID == approvalflow.OptionAllow {
			return true, hitlservice.DecisionApproved, nil
		}
		return false, hitlservice.DecisionDenied, nil
	}
	return false, hitlservice.DecisionDenied, nil
}

func (t *Transport) withdrawCard(ctx context.Context, sid libacp.SessionID, toolCallID string, d askOutcome) {
	if d.err != nil {
		return
	}
	meta := approvalMetaFor(d.verdict.DecidedBy, d.verdict.Approved)
	content := libacp.NewTextContent(meta.Note)
	t.sendUpdate(ctx, libacp.SessionNotification{
		SessionID: sid,
		Update: libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateToolCallUpdate,
			ToolCallID:    toolCallID,
			ToolContent:   []libacp.ToolCallContent{{Type: libacp.ToolCallContentRegular, Content: &content}},
			Meta:          mergeMeta(nil, approvalMetaKey, meta),
		},
	})
}

func approvalMetaFor(decidedBy string, approved bool) approvalMeta {
	by := strings.ToLower(decidedBy)
	note := elsewhereDeniedNote
	switch {
	case by == hitlservice.DecidedByDeadline:
		note = askExpiredNote
	case strings.Contains(by, decidedByOracle) && approved:
		note = oracleApprovedNote
	case strings.Contains(by, decidedByOracle):
		note = oracleDeniedNote
	case approved:
		note = elsewhereApprovedNote
	}
	return approvalMeta{DecidedBy: by, Approved: approved, Note: note}
}

// recordApprovalDecision writes the interactive decision to hitl_approvals
// through the durable approval service. Recording never changes the outcome of
// the ask: a failure is logged and the decision stands.
func (t *Transport) recordApprovalDecision(ctx context.Context, req hitlservice.ApprovalRequest, contenoxSessionID string, outcome hitlservice.DecisionOutcome, askedAt time.Time) {
	t.recordDecisionBy(ctx, req, contenoxSessionID, outcome, askedAt, decidedByACPClient)
}

func (t *Transport) recordDecisionBy(ctx context.Context, req hitlservice.ApprovalRequest, contenoxSessionID string, outcome hitlservice.DecisionOutcome, askedAt time.Time, decidedBy string) {
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
		DecidedBy: decidedBy,
		AskedAt:   askedAt,
	}); err != nil {
		slog.Warn("acpsvc: could not record approval decision", "tool", req.ToolName, "outcome", string(outcome), "error", err)
	}
}
