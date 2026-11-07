package acpsvc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type approvalRowStore interface {
	ListHITLApprovalsForSession(ctx context.Context, sessionID string, limit int) ([]*runtimetypes.HITLApproval, error)
}

func (t *Transport) replayApprovalDecisions(ctx context.Context, store approvalRowStore, contenoxSessionID string) map[string]approvalMeta {
	if store == nil || contenoxSessionID == "" {
		return nil
	}
	rows, err := store.ListHITLApprovalsForSession(ctx, contenoxSessionID, 0)
	if err != nil {
		slog.Warn("acpsvc: could not read the session's approvals for replay", "session", contenoxSessionID, "error", err)
		return nil
	}
	return approvalDecisionsByToolCall(rows)
}

func approvalDecisionsByToolCall(rows []*runtimetypes.HITLApproval) map[string]approvalMeta {
	out := map[string]approvalMeta{}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row == nil || row.ToolCallID == "" || row.State == runtimetypes.HITLApprovalPending {
			continue
		}
		approved, by := resolutionVerdict(row)
		if by == decidedByACPClient {
			continue
		}
		out[row.ToolCallID] = approvalMetaFor(by, approved)
	}
	return out
}

func resolutionVerdict(row *runtimetypes.HITLApproval) (bool, string) {
	var res struct {
		Approved  *bool  `json:"approved"`
		DecidedBy string `json:"decidedBy"`
	}
	if len(row.Resolution) > 0 {
		_ = json.Unmarshal(row.Resolution, &res)
	}
	approved := row.State == runtimetypes.HITLApprovalApproved
	if res.Approved != nil {
		approved = *res.Approved
	}
	by := strings.ToLower(strings.TrimSpace(res.DecidedBy))
	if row.State == runtimetypes.HITLApprovalExpired {
		by = hitlservice.DecidedByDeadline
	}
	return approved, by
}
