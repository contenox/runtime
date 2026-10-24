package hitlservice_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_RequestAttention_ReturnsTheOperatorsWords(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	answered := make(chan string, 1)
	go func() {
		text, err := svc.RequestAttention(ctx, hitlservice.AttentionRequest{
			Summary:   "which project did you mean?",
			Detail:    "the intent named none",
			MissionID: "m-1",
			AgentName: "chain-acp",
		}, taskengine.NoopTaskEventSink{})
		require.NoError(t, err)
		answered <- text
	}()

	var ask *runtimetypes.HITLApproval
	require.Eventually(t, func() bool {
		rows, err := svc.ListPending(ctx, 10)
		require.NoError(t, err)
		if len(rows) != 1 {
			return false
		}
		ask = rows[0]
		return true
	}, 5*time.Second, 10*time.Millisecond, "the unit's question must be listed as a pending ask")

	require.True(t, hitlservice.IsAttentionAsk(ask),
		"the row must be recognisable as a question, not a permission gate — answering it with a bare approve would leave the unit with nothing")
	require.Equal(t, "which project did you mean?", ask.ArgsSummary)
	require.NotNil(t, ask.MissionID)
	require.Equal(t, "m-1", *ask.MissionID)

	require.NoError(t, svc.Answer(ctx, ask.ID, "the contenox runtime repo at /home/x/src/contenox"))

	select {
	case text := <-answered:
		require.Equal(t, "the contenox runtime repo at /home/x/src/contenox", text)
	case <-time.After(5 * time.Second):
		t.Fatal("the asking unit was never woken with the answer")
	}

	resolved, err := store.GetHITLApproval(ctx, ask.ID)
	require.NoError(t, err)
	require.Equal(t, runtimetypes.HITLApprovalApproved, resolved.State)
	require.Equal(t, "the contenox runtime repo at /home/x/src/contenox", hitlservice.AnswerOf(resolved))
}

func TestUnit_Answer_RefusesAPermissionAsk(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	go func() {
		_, _ = svc.RequestApproval(ctx, hitlservice.ApprovalRequest{
			ToolsName: "local_fs",
			ToolName:  "write_file",
			Args:      map[string]any{"path": "/tmp/x"},
		}, taskengine.NoopTaskEventSink{})
	}()

	var ask *runtimetypes.HITLApproval
	require.Eventually(t, func() bool {
		rows, err := svc.ListPending(ctx, 10)
		require.NoError(t, err)
		if len(rows) != 1 {
			return false
		}
		ask = rows[0]
		return true
	}, 5*time.Second, 10*time.Millisecond)

	require.False(t, hitlservice.IsAttentionAsk(ask))
	err := svc.Answer(ctx, ask.ID, "go ahead")
	require.Error(t, err)
	require.Contains(t, err.Error(), "approve/deny")
}

func TestUnit_Answer_RejectsEmptyText(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)
	require.Error(t, svc.Answer(ctx, "any-id", "   "))
}

func TestUnit_Answer_UnknownIDReturnsNotFound(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)
	require.ErrorIs(t, svc.Answer(ctx, "no-such-ask", "text"), hitlservice.ErrApprovalNotFound)
}

func TestUnit_RequestAttention_CeilingEndsTheWait(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)
	hitlservice.SetApprovalCeiling(svc, 150*time.Millisecond)

	start := time.Now()
	text, err := svc.RequestAttention(ctx, hitlservice.AttentionRequest{
		Summary:   "anyone there?",
		MissionID: "m-2",
	}, taskengine.NoopTaskEventSink{})
	require.ErrorIs(t, err, hitlservice.ErrAttentionUnanswered)
	require.Empty(t, text)
	require.Less(t, time.Since(start), 5*time.Second, "the wait must be bounded by the ceiling")
}

func TestUnit_RequestAttention_RequiresASummary(t *testing.T) {
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)
	_, err := svc.RequestAttention(ctx, hitlservice.AttentionRequest{Summary: "  "}, taskengine.NoopTaskEventSink{})
	require.Error(t, err)
}

func TestUnit_RequestAttention_AnswerFromAnotherProcessWakesTheUnit(t *testing.T) {
	ctx, store, dbPath := setupHITLDB(t)
	unitSvc := newDurableService(t, store)

	answered := make(chan string, 1)
	go func() {
		text, err := unitSvc.RequestAttention(ctx, hitlservice.AttentionRequest{
			Summary:   "which project did you mean?",
			MissionID: "m-cross",
		}, taskengine.NoopTaskEventSink{})
		require.NoError(t, err)
		answered <- text
	}()

	operatorCtx, operatorStore := reopenHITLDB(t, dbPath)
	operatorSvc := newDurableService(t, operatorStore)

	var askID string
	require.Eventually(t, func() bool {
		rows, err := operatorSvc.ListPending(operatorCtx, 10)
		require.NoError(t, err)
		if len(rows) != 1 {
			return false
		}
		askID = rows[0].ID
		return true
	}, 5*time.Second, 10*time.Millisecond, "the other process must see the question as pending")

	require.NoError(t, operatorSvc.Answer(operatorCtx, askID, "the runtime repo"))

	select {
	case text := <-answered:
		require.Equal(t, "the runtime repo", text)
	case <-time.After(10 * time.Second):
		t.Fatal("a unit waiting in another process was never woken by the answer")
	}
}
