package acpsvc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func approvalRow(toolCallID string, state runtimetypes.HITLApprovalState, resolution string) *runtimetypes.HITLApproval {
	row := &runtimetypes.HITLApproval{
		ID:         toolCallID + "-ask",
		ToolsName:  "local_shell",
		ToolName:   "local_shell",
		State:      state,
		ToolCallID: toolCallID,
	}
	if resolution != "" {
		row.Resolution = json.RawMessage(resolution)
	}
	return row
}

func TestApprovalDecisionsByToolCall(t *testing.T) {
	decisions := approvalDecisionsByToolCall([]*runtimetypes.HITLApproval{
		approvalRow("inbox-yes", runtimetypes.HITLApprovalApproved, `{"approved":true,"decidedBy":"inbox"}`),
		approvalRow("oracle-yes", runtimetypes.HITLApprovalApproved, `{"approved":true,"decidedBy":"agent:oracle"}`),
		approvalRow("oracle-no", runtimetypes.HITLApprovalDenied, `{"approved":false,"decidedBy":"agent:oracle"}`),
		approvalRow("expired", runtimetypes.HITLApprovalExpired, `{"approved":false}`),
		approvalRow("in-chat", runtimetypes.HITLApprovalApproved, `{"approved":true,"decidedBy":"acp_client"}`),
		approvalRow("still-open", runtimetypes.HITLApprovalPending, ""),
		approvalRow("", runtimetypes.HITLApprovalApproved, `{"approved":true,"decidedBy":"inbox"}`),
	})

	require.Equal(t, approvalMeta{DecidedBy: "inbox", Approved: true, Note: elsewhereApprovedNote}, decisions["inbox-yes"])
	require.Equal(t, approvalMeta{DecidedBy: "agent:oracle", Approved: true, Note: oracleApprovedNote}, decisions["oracle-yes"])
	require.Equal(t, approvalMeta{DecidedBy: "agent:oracle", Approved: false, Note: oracleDeniedNote}, decisions["oracle-no"])
	require.Equal(t, approvalMeta{DecidedBy: "deadline", Approved: false, Note: askExpiredNote}, decisions["expired"])
	require.NotContains(t, decisions, "in-chat", "an answer given on the card needs no note")
	require.NotContains(t, decisions, "still-open")
	require.NotContains(t, decisions, "")
	require.Len(t, decisions, 4)
}

func TestLoopback_ReplayAttachesTheApprovalDecisionToItsStep(t *testing.T) {
	h := newLoopbackHarness(t)

	h.tr.replayMessages(context.Background(), "sess-1", []taskengine.Message{
		{Role: "assistant", CallTools: []taskengine.ToolCall{{ID: "call-7", Function: taskengine.FunctionCall{Name: "local_shell.local_shell", Arguments: `{"command":"npm install"}`}}}},
		{Role: "tool", ToolCallID: "call-7", Content: `{"exit_code":0}`},
		{Role: "tool", ToolCallID: "call-8", Content: `{"exit_code":0}`},
	}, false, map[string]approvalMeta{"call-7": approvalMetaFor("inbox", true)})

	var got []libacp.SessionUpdate
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case n := <-h.lc.updates:
			got = append(got, n.Update)
		case <-deadline:
			t.Fatalf("expected 3 replayed updates, got %d", len(got))
		}
	}

	require.Equal(t, libacp.SessionUpdateToolCall, got[0].SessionUpdate)
	require.Empty(t, got[0].Meta, "the call itself carries nothing; the verdict sits on the result")

	require.Equal(t, libacp.SessionUpdateToolCallUpdate, got[1].SessionUpdate)
	require.Equal(t, "call-7", got[1].ToolCallID)
	var meta map[string]approvalMeta
	require.NoError(t, json.Unmarshal(got[1].Meta, &meta))
	require.Equal(t, approvalMeta{DecidedBy: "inbox", Approved: true, Note: elsewhereApprovedNote}, meta[approvalMetaKey])

	require.Equal(t, "call-8", got[2].ToolCallID)
	require.Empty(t, got[2].Meta, "a step nobody decided from outside stays bare")
}
