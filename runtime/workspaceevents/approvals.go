package workspaceevents

import (
	"context"
	"encoding/json"
	"time"

	"github.com/contenox/runtime/runtime/runtimetypes"
)

type SessionLookup func(ctx context.Context, internalSessionID string) (session, cwd string)

func ApprovalObserver(events Service, lookup SessionLookup) func(ctx context.Context, approval runtimetypes.HITLApproval, approved bool) {
	return func(ctx context.Context, approval runtimetypes.HITLApproval, approved bool) {
		if events == nil || lookup == nil {
			return
		}
		session, cwd := lookup(ctx, approval.SessionID)
		if cwd == "" {
			return
		}
		root, err := events.GrantedRoot(cwd)
		if err != nil {
			return
		}
		decision := "denied"
		if approved {
			decision = "approved"
		}
		data := map[string]any{
			"decision": decision,
			"tool":     approval.ToolsName + "." + approval.ToolName,
		}
		if session != "" {
			data["sessionId"] = session
		}
		if approval.ToolCallID != "" {
			data["toolCallId"] = approval.ToolCallID
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return
		}
		appendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = events.Append(appendCtx, root, Input{
			Type:    TypeApprovalDecided,
			Source:  SourceApproval,
			Subject: approval.ID,
			Data:    raw,
		})
	}
}
