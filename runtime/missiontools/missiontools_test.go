package missiontools_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (context.Context, missionservice.Service, string) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "missiontools.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc := missionservice.New(db)
	m := &missionservice.Mission{Intent: "ship the board", AgentName: "runner", HITLPolicyName: "default"}
	require.NoError(t, svc.Create(ctx, m))
	return ctx, svc, m.ID
}

func reportCall(kind, summary string) *taskengine.ToolsCall {
	return &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameReport,
		Args:     map[string]string{"kind": kind, "summary": summary},
	}
}

func TestUnit_MissionTools_ReportFilesAgainstBoundMission(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	toolCtx := missiontools.WithMissionID(ctx, missionID)
	out, dt, err := tools.Exec(toolCtx, time.Now(), nil, false, reportCall("finding", "found the leak"))
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, dt)
	require.Contains(t, out, "finding")

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, missionservice.ReportKindFinding, reports[0].Kind)
	require.Equal(t, "found the leak", reports[0].Summary)
	require.Equal(t, missionID, reports[0].MissionID)

	m, err := svc.Get(ctx, missionID)
	require.NoError(t, err)
	require.NotNil(t, m.LastHeartbeat, "filing a report is proof of life and heartbeats the mission")
}

func TestUnit_MissionTools_AbsentWithoutMission(t *testing.T) {
	ctx, svc, _ := setup(t)
	tools := missiontools.New(svc, nil)

	listed, err := tools.GetToolsForToolsByName(ctx, missiontools.ToolsProviderName)
	require.NoError(t, err)
	require.Empty(t, listed, "off a mission the tools are absent from the tool list")

	_, _, err = tools.Exec(ctx, time.Now(), nil, false, reportCall("progress", "should not run"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "only available to a unit dispatched on a mission")
}

func TestUnit_MissionTools_ExposedOnMission(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	listed, err := tools.GetToolsForToolsByName(missiontools.WithMissionID(ctx, missionID), missiontools.ToolsProviderName)
	require.NoError(t, err)
	require.Len(t, listed, 4)
	names := make([]string, len(listed))
	for i, tool := range listed {
		names[i] = tool.Function.Name
	}
	require.ElementsMatch(t, []string{
		missiontools.ToolNameReport,
		missiontools.ToolNameAskAttention,
		missiontools.ToolNamePlan,
		missiontools.ToolNameFinish,
	}, names)
}

func TestUnit_MissionTools_ReportScopedToOwnMission(t *testing.T) {
	ctx, svc, missionA := setup(t)
	other := &missionservice.Mission{Intent: "other work", AgentName: "runner", HITLPolicyName: "default"}
	require.NoError(t, svc.Create(ctx, other))

	tools := missiontools.New(svc, nil)
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionA), time.Now(), nil, false, reportCall("progress", "mine"))
	require.NoError(t, err)

	a, err := svc.ListReports(ctx, missionA, 10)
	require.NoError(t, err)
	require.Len(t, a, 1)
	b, err := svc.ListReports(ctx, other.ID, 10)
	require.NoError(t, err)
	require.Empty(t, b, "a unit cannot report on a mission that is not its own")
}

func TestUnit_MissionTools_ReportDefaultsKind(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameReport,
		Args:     map[string]string{"summary": "just checking in"},
	}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, missionservice.ReportKindProgress, reports[0].Kind)
}

func TestUnit_MissionTools_ReportReadsModelArgs(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	input := map[string]any{
		"kind":    "result",
		"summary": "done",
		"detail":  "all green",
		"refs":    []any{"README.md", "/tmp/out.log"},
	}
	call := &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNameReport}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, missionservice.ReportKindResult, reports[0].Kind)
	require.Equal(t, "all green", reports[0].Detail)
	require.Equal(t, []string{"README.md", "/tmp/out.log"}, reports[0].Refs)
}

func TestUnit_MissionTools_ReportReadsModelHandover(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	input := map[string]any{
		"kind":    "result",
		"summary": "hot loop ported",
		"handover": map[string]any{
			"outcome":         "ported the hot loop; benchmarks pending",
			"artifacts":       []any{"src/hotloop.rs"},
			"handoverForNext": "pick up the benchmark harness",
			"caveats":         "SIMD path untested on aarch64",
		},
	}
	call := &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNameReport}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.NotNil(t, reports[0].Handover)
	require.Equal(t, "ported the hot loop; benchmarks pending", reports[0].Handover.Outcome)
	require.Equal(t, []string{"src/hotloop.rs"}, reports[0].Handover.Artifacts)
	require.Equal(t, "pick up the benchmark harness", reports[0].Handover.HandoverForNext)
	require.Equal(t, "SIMD path untested on aarch64", reports[0].Handover.Caveats)
}

func TestUnit_MissionTools_ReportReadsDeterministicHandover(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameReport,
		Args: map[string]string{
			"kind":     "result",
			"summary":  "done",
			"handover": `{"outcome":"shipped","handoverForNext":"wire the inbox next"}`,
		},
	}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.NotNil(t, reports[0].Handover)
	require.Equal(t, "shipped", reports[0].Handover.Outcome)
	require.Equal(t, "wire the inbox next", reports[0].Handover.HandoverForNext)
}

func TestUnit_MissionTools_ReportWithoutHandoverIsLegacy(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, reportCall("progress", "no hand-off here"))
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Nil(t, reports[0].Handover, "a report with no hand-off carries a nil Handover")
}

func TestUnit_MissionTools_ReportRejectsMalformedHandover(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameReport,
		Args:     map[string]string{"kind": "result", "summary": "done", "handover": `{not json`},
	}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "handover")
}

func TestUnit_MissionTools_InvalidKindRejected(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, reportCall("gossip", "nope"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid report kind")
}

type fakeAsker struct {
	missionID string
	summary   string
	detail    string
	calls     int
	answer    string
	err       error
}

func (f *fakeAsker) RaiseAttention(_ context.Context, missionID, summary, detail string) (string, error) {
	f.calls++
	f.missionID = missionID
	f.summary = summary
	f.detail = detail
	return f.answer, f.err
}

func TestUnit_MissionTools_AskAttentionUsesAskerWhenWired(t *testing.T) {
	ctx, svc, missionID := setup(t)
	asker := &fakeAsker{answer: "staging, and use the seeded fixtures"}
	tools := missiontools.New(svc, asker)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameAskAttention,
		Args:     map[string]string{"summary": "need a decision", "detail": "prod or staging?"},
	}
	out, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err)
	require.Equal(t, "staging, and use the seeded fixtures", out)
	require.Equal(t, 1, asker.calls)
	require.Equal(t, missionID, asker.missionID)
	require.Equal(t, "need a decision", asker.summary)
	require.Equal(t, "prod or staging?", asker.detail)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Empty(t, reports, "a wired asker does not double-write a blocker report")
}

func TestUnit_MissionTools_AskAttentionFallsBackToBlocker(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameAskAttention,
		Args:     map[string]string{"summary": "need a decision"},
	}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err)

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, missionservice.ReportKindBlocker, reports[0].Kind)
	require.Equal(t, "need a decision", reports[0].Summary)
}

func TestUnit_MissionTools_UnknownMissionSurfacesError(t *testing.T) {
	ctx, svc, _ := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, "no-such-mission"), time.Now(), nil, false, reportCall("progress", "ghost"))
	require.Error(t, err)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_MissionTools_MetaRoundTrip(t *testing.T) {
	raw := missionservice.MarshalMissionMeta("mission-123")
	require.NotNil(t, raw)
	id, ok := missionservice.ParseMissionMeta(raw)
	require.True(t, ok)
	require.Equal(t, "mission-123", id)

	_, ok = missionservice.ParseMissionMeta([]byte(`{"contenox.agent":"runner"}`))
	require.False(t, ok, "an unrelated _meta is not a mission")

	require.Nil(t, missionservice.MarshalMissionMeta("  "), "a blank id marshals to no _meta")
}

func planModelCall(explanation string, entries ...map[string]any) (*taskengine.ToolsCall, map[string]any) {
	items := make([]any, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	input := map[string]any{"entries": items}
	if explanation != "" {
		input["explanation"] = explanation
	}
	return &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNamePlan}, input
}

func planEntryArg(content, status, priority string) map[string]any {
	return map[string]any{"content": content, "status": status, "priority": priority}
}

func TestUnit_MissionTools_PlanSetsSnapshotAndEchoesIDs(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call, input := planModelCall("first cut",
		planEntryArg("survey the codebase", "in_progress", "high"),
		planEntryArg("port the hot loop", "pending", "medium"),
	)
	out, dt, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeJSON, dt)

	echoed, ok := out.(missionservice.Plan)
	require.True(t, ok, "mission_plan echoes the stored Plan so ids carry forward")
	require.Equal(t, 1, echoed.Revision)
	require.Len(t, echoed.Entries, 2)
	require.NotEmpty(t, echoed.Entries[0].ID)
	require.NotEmpty(t, echoed.Entries[1].ID)
	require.Equal(t, "first cut", echoed.Explanation)

	m, err := svc.Get(ctx, missionID)
	require.NoError(t, err)
	require.Equal(t, 1, m.Plan.Revision)
	require.Len(t, m.Plan.Entries, 2)
	require.Equal(t, "survey the codebase", m.Plan.Entries[0].Content)
	require.Equal(t, missionservice.PlanEntryInProgress, m.Plan.Entries[0].Status)
	require.Equal(t, missionservice.PlanEntryPriorityHigh, m.Plan.Entries[0].Priority)

	require.NotNil(t, m.LastHeartbeat, "revising the plan is proof of life and heartbeats the mission")
}

func TestUnit_MissionTools_PlanAbsentOffMission(t *testing.T) {
	ctx, svc, _ := setup(t)
	tools := missiontools.New(svc, nil)

	call, input := planModelCall("", planEntryArg("do a thing", "pending", "low"))
	_, _, err := tools.Exec(ctx, time.Now(), input, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only available to a unit dispatched on a mission")
}

func TestUnit_MissionTools_PlanScopedToOwnMission(t *testing.T) {
	ctx, svc, missionA := setup(t)
	other := &missionservice.Mission{Intent: "other work", AgentName: "runner", HITLPolicyName: "default"}
	require.NoError(t, svc.Create(ctx, other))

	tools := missiontools.New(svc, nil)
	call, input := planModelCall("mine", planEntryArg("only on A", "pending", "high"))
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionA), time.Now(), input, false, call)
	require.NoError(t, err)

	a, err := svc.Get(ctx, missionA)
	require.NoError(t, err)
	require.Equal(t, 1, a.Plan.Revision)
	b, err := svc.Get(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, 0, b.Plan.Revision, "a unit cannot plan on a mission that is not its own")
}

func TestUnit_MissionTools_PlanReadsDeterministicJSONArgs(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNamePlan,
		Args: map[string]string{
			"entries":     `[{"content":"step one","status":"in_progress","priority":"high"},{"content":"step two","status":"pending","priority":"low"}]`,
			"explanation": "seed from a deterministic chain",
		},
	}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err)

	m, err := svc.Get(ctx, missionID)
	require.NoError(t, err)
	require.Len(t, m.Plan.Entries, 2)
	require.Equal(t, "step one", m.Plan.Entries[0].Content)
	require.Equal(t, missionservice.PlanEntryInProgress, m.Plan.Entries[0].Status)
	require.Equal(t, "seed from a deterministic chain", m.Plan.Explanation)
}

func TestUnit_MissionTools_PlanRejectsEmptySnapshot(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNamePlan}
	input := map[string]any{"entries": []any{}}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one entry")
}

func TestUnit_MissionTools_PlanRejectsUnknownStatus(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call, input := planModelCall("", planEntryArg("bad status", "halfway", "high"))
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid plan entry status")
}

func TestUnit_MissionTools_PlanMissingEntriesRejected(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNamePlan}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), map[string]any{}, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires an 'entries' array")
}

func TestUnit_MissionTools_PlanCarriesIDsForwardAndGuardsCompleted(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)
	mctx := missiontools.WithMissionID(ctx, missionID)

	call, input := planModelCall("start", planEntryArg("wire the seam", "in_progress", "high"))
	out, _, err := tools.Exec(mctx, time.Now(), input, false, call)
	require.NoError(t, err)
	id := out.(missionservice.Plan).Entries[0].ID
	require.NotEmpty(t, id)

	call, input = planModelCall("done that step", map[string]any{
		"id": id, "content": "wire the seam", "status": "completed", "priority": "high",
	})
	out, _, err = tools.Exec(mctx, time.Now(), input, false, call)
	require.NoError(t, err)
	require.Equal(t, 2, out.(missionservice.Plan).Revision)
	require.Equal(t, id, out.(missionservice.Plan).Entries[0].ID, "the entry keeps its identity across revisions")

	call, input = planModelCall("oops", map[string]any{
		"id": id, "content": "wire the seam differently", "status": "completed", "priority": "high",
	})
	_, _, err = tools.Exec(mctx, time.Now(), input, false, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already-completed work")
}

func finishCall(status, reason string) *taskengine.ToolsCall {
	return &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameFinish,
		Args:     map[string]string{"status": status, "reason": reason},
	}
}

func TestUnit_MissionTools_FinishSetsTerminalStatus(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	out, dt, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, finishCall("landed", "shipped it"))
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, dt)
	require.Contains(t, out, "landed")

	m, err := svc.Get(ctx, missionID)
	require.NoError(t, err)
	require.Equal(t, missionservice.StatusLanded, m.Status)
	require.Equal(t, "shipped it", m.StatusReason)
	require.NotNil(t, m.LastHeartbeat, "finishing a mission heartbeats it")
}

func TestUnit_MissionTools_FinishReadsModelStuck(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	call := &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNameFinish}
	input := map[string]any{"status": "stuck", "reason": "cannot decide alone"}
	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), input, false, call)
	require.NoError(t, err)

	m, err := svc.Get(ctx, missionID)
	require.NoError(t, err)
	require.Equal(t, missionservice.StatusStuck, m.Status)
}

func TestUnit_MissionTools_FinishRequiresStatus(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, finishCall("", "no status"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a status")
}

func TestUnit_MissionTools_FinishRejectsNonTerminal(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, finishCall("open", "not terminal"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "terminal status is required")
}

func TestUnit_MissionTools_FinishIdempotentThenConflict(t *testing.T) {
	ctx, svc, missionID := setup(t)
	tools := missiontools.New(svc, nil)
	mctx := missiontools.WithMissionID(ctx, missionID)

	_, _, err := tools.Exec(mctx, time.Now(), nil, false, finishCall("landed", "done"))
	require.NoError(t, err)

	_, _, err = tools.Exec(mctx, time.Now(), nil, false, finishCall("landed", "done again"))
	require.NoError(t, err)

	_, _, err = tools.Exec(mctx, time.Now(), nil, false, finishCall("derailed", "reversal"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "already finished")
}

func TestUnit_MissionTools_FinishAbsentOffMission(t *testing.T) {
	ctx, svc, _ := setup(t)
	tools := missiontools.New(svc, nil)

	_, _, err := tools.Exec(ctx, time.Now(), nil, false, finishCall("landed", "should not run"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "only available to a unit dispatched on a mission")
}

func TestUnit_MissionTools_AskAttentionFallsBackWhenUnanswered(t *testing.T) {
	ctx, svc, missionID := setup(t)
	asker := &fakeAsker{err: errors.New("attention ask went unanswered")}
	tools := missiontools.New(svc, asker)

	call := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNameAskAttention,
		Args:     map[string]string{"summary": "which project?", "detail": "the intent named none"},
	}
	out, _, err := tools.Exec(missiontools.WithMissionID(ctx, missionID), time.Now(), nil, false, call)
	require.NoError(t, err, "an unanswered ask is a fallback, never a tool failure")
	require.Contains(t, out, "no operator answered")

	reports, err := svc.ListReports(ctx, missionID, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1, "the question survives as a durable blocker")
	require.Equal(t, missionservice.ReportKindBlocker, reports[0].Kind)
	require.Equal(t, "which project?", reports[0].Summary)
	require.Contains(t, reports[0].Detail, "the intent named none", "the original detail is kept")
	require.Contains(t, reports[0].Detail, "got no answer", "…and says an answer was solicited and missed")
}
