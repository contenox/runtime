package hitlservice

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

// DecisionOutcome is how an interactive approval ask ended.
type DecisionOutcome string

const (
	DecisionApproved DecisionOutcome = "approved"
	DecisionDenied   DecisionOutcome = "denied"
	// DecisionTimedOut means nobody answered before the rule's deadline.
	DecisionTimedOut DecisionOutcome = "timeout"
	// DecisionCancelled means the ask was abandoned (turn cancelled, client gone
	// or the client answered "cancelled"); nothing was approved.
	DecisionCancelled DecisionOutcome = "cancelled"
)

// Decision describes a finished interactive ask, for the audit record.
type Decision struct {
	Outcome   DecisionOutcome
	DecidedBy string // for example "acp_client"
	AskedAt   time.Time
}

// DecisionRecorder writes a resolved hitl_approvals row for an ask that was
// answered outside the durable RequestApproval flow (for example by an ACP
// client through session/request_permission). It records after the fact and
// never changes the approval outcome.
type DecisionRecorder interface {
	RecordDecision(ctx context.Context, req ApprovalRequest, d Decision) error
}

var _ DecisionRecorder = (*service)(nil)

func (s *service) RecordDecision(ctx context.Context, req ApprovalRequest, d Decision) error {
	if s.approvals == nil {
		return fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	now := time.Now().UTC()
	asked := d.AskedAt.UTC()
	if d.AskedAt.IsZero() || asked.After(now) {
		asked = now
	}

	var (
		state    runtimetypes.HITLApprovalState
		approved bool
		reason   string
	)
	switch d.Outcome {
	case DecisionApproved:
		state, approved = runtimetypes.HITLApprovalApproved, true
	case DecisionTimedOut:
		state, reason = runtimetypes.HITLApprovalExpired, string(DecisionTimedOut)
		approved = onTimeoutOutcome(req.OnTimeout)
	case DecisionCancelled:
		state, reason = runtimetypes.HITLApprovalDenied, string(DecisionCancelled)
	default:
		state = runtimetypes.HITLApprovalDenied
	}

	onTimeout := req.OnTimeout
	if onTimeout == "" {
		onTimeout = ActionDeny
	}
	// A zero expires_at means "no deadline", the same convention the sweeper
	// uses; the row is already resolved, so it never matters to the sweeper.
	var expires time.Time
	if req.TimeoutS > 0 {
		expires = asked.Add(time.Duration(req.TimeoutS) * time.Second)
	}

	resolution := approvalResolution{Approved: &approved}
	if d.DecidedBy != "" {
		by := d.DecidedBy
		resolution.DecidedBy = &by
	}
	if reason != "" {
		resolution.Reason = &reason
	}
	raw, err := marshalResolution(resolution)
	if err != nil {
		return err
	}

	row := &runtimetypes.HITLApproval{
		ID:          uuid.NewString(),
		ToolsName:   req.ToolsName,
		ToolName:    req.ToolName,
		ArgsSummary: summarizeApprovalArgs(req.Args),
		PolicyName:  req.PolicyName,
		MatchedRule: req.MatchedRule,
		OnTimeout:   string(onTimeout),
		State:       state,
		Resolution:  raw,
		InstanceID:  req.InstanceID,
		SessionID:   req.SessionID,
		AgentName:   req.AgentName,
		CreatedAt:   asked,
		ExpiresAt:   expires,
		ResolvedAt:  &now,
	}
	if req.MissionID != "" {
		missionID := req.MissionID
		row.MissionID = &missionID
	}
	if req.Diff != "" {
		diff := req.Diff
		row.Diff = &diff
	}
	if err := s.approvals.CreateHITLApproval(ctx, row); err != nil {
		return fmt.Errorf("hitlservice: record approval decision: %w", err)
	}
	return nil
}
