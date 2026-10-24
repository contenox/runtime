package acpsvc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func planSnapshotJSON(t *testing.T, plan missionservice.Plan) string {
	t.Helper()
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	return string(raw)
}

func TestUnit_PlanProjection_FullSnapshotCast(t *testing.T) {
	plan := missionservice.Plan{
		Revision: 2,
		Entries: []missionservice.PlanEntry{
			{ID: "a", Content: "survey the codebase", Status: missionservice.PlanEntryCompleted, Priority: missionservice.PlanEntryPriorityHigh},
			{ID: "b", Content: "port the hot loop", Status: missionservice.PlanEntryInProgress, Priority: missionservice.PlanEntryPriorityMedium},
			{ID: "c", Content: "benchmark", Status: missionservice.PlanEntryPending, Priority: missionservice.PlanEntryPriorityLow},
		},
	}
	ev := taskengine.TaskEvent{
		Kind:     taskengine.TaskEventToolCall,
		ToolName: planToolEventName,
		Content:  planSnapshotJSON(t, plan),
	}

	note, ok := planUpdateNotification(libacp.SessionID("sess-1"), ev)
	require.True(t, ok)
	require.Equal(t, libacp.SessionID("sess-1"), note.SessionID)
	require.Equal(t, libacp.SessionUpdatePlan, note.Update.SessionUpdate)
	require.Len(t, note.Update.Entries, 3, "the projection is a FULL snapshot — the entire entries list")

	require.Equal(t, libacp.PlanEntry{Content: "survey the codebase", Status: libacp.PlanStatusCompleted, Priority: libacp.PlanPriorityHigh}, note.Update.Entries[0])
	require.Equal(t, libacp.PlanEntry{Content: "port the hot loop", Status: libacp.PlanStatusInProgress, Priority: libacp.PlanPriorityMedium}, note.Update.Entries[1])
	require.Equal(t, libacp.PlanEntry{Content: "benchmark", Status: libacp.PlanStatusPending, Priority: libacp.PlanPriorityLow}, note.Update.Entries[2])

	wire, err := json.Marshal(note.Update)
	require.NoError(t, err)
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &generic))
	require.Equal(t, `"plan"`, string(generic["sessionUpdate"]))
	require.Contains(t, generic, "entries")
}

func TestUnit_PlanProjection_IgnoresNonPlanAndFailedEvents(t *testing.T) {
	goodContent := planSnapshotJSON(t, missionservice.Plan{
		Revision: 1,
		Entries:  []missionservice.PlanEntry{{ID: "a", Content: "do", Status: missionservice.PlanEntryPending, Priority: missionservice.PlanEntryPriorityHigh}},
	})

	cases := []struct {
		name string
		ev   taskengine.TaskEvent
	}{
		{"a different tool", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "mission.mission_report", Content: goodContent}},
		{"the report tool", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.write_file", Content: goodContent}},
		{"a plan call that errored", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: planToolEventName, Content: goodContent, Error: "set plan: boom"}},
		{"unparseable content", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: planToolEventName, Content: "not json {"}},
		{"empty content", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: planToolEventName, Content: ""}},
		{"parses but has no entries", taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: planToolEventName, Content: `{"revision":3,"entries":[]}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := planUpdateNotification(libacp.SessionID("sess-1"), c.ev)
			require.False(t, ok, "no plan update should be emitted")
		})
	}
}

func TestUnit_PlanProjection_EnumParity(t *testing.T) {
	require.Equal(t, string(libacp.PlanStatusPending), string(missionservice.PlanEntryPending))
	require.Equal(t, string(libacp.PlanStatusInProgress), string(missionservice.PlanEntryInProgress))
	require.Equal(t, string(libacp.PlanStatusCompleted), string(missionservice.PlanEntryCompleted))

	require.Equal(t, string(libacp.PlanPriorityHigh), string(missionservice.PlanEntryPriorityHigh))
	require.Equal(t, string(libacp.PlanPriorityMedium), string(missionservice.PlanEntryPriorityMedium))
	require.Equal(t, string(libacp.PlanPriorityLow), string(missionservice.PlanEntryPriorityLow))
}

func TestUnit_PlanProjection_ReachesAttachedViewer(t *testing.T) {
	ctx := context.Background()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "plan-projection.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	missions := missionservice.New(db)
	mission := &missionservice.Mission{Intent: "migrate the loop", AgentName: "planner", HITLPolicyName: "default"}
	require.NoError(t, missions.Create(ctx, mission))

	tools := missiontools.New(missions, nil)
	planCall := &taskengine.ToolsCall{
		Name:     missiontools.ToolsProviderName,
		ToolName: missiontools.ToolNamePlan,
		Args: map[string]string{
			"entries":     `[{"content":"survey the codebase","status":"in_progress","priority":"high"},{"content":"port the hot loop","status":"pending","priority":"medium"}]`,
			"explanation": "first cut",
		},
	}
	result, resultType, err := tools.Exec(missiontools.WithMissionID(ctx, mission.ID), time.Now(), nil, false, planCall)
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeJSON, resultType)

	stored, err := missions.Get(ctx, mission.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Plan.Revision)
	require.Len(t, stored.Plan.Entries, 2)

	echoed := result.(missionservice.Plan)
	ev := taskengine.TaskEvent{
		Kind:       taskengine.TaskEventToolCall,
		ToolName:   planToolEventName,
		ApprovalID: "plan-call-1",
		Content:    planSnapshotJSON(t, echoed),
	}
	payload, err := json.Marshal(ev)
	require.NoError(t, err)

	h := newLoopbackHarness(t)
	sid := libacp.SessionID("unit-session")
	h.tr.publishEvent(ctx, sid, payload)

	var planNote *libacp.SessionNotification
	for _, n := range h.lc.drain(t, 2) {
		if n.Update.SessionUpdate == libacp.SessionUpdatePlan {
			nn := n
			planNote = &nn
			break
		}
	}
	require.NotNil(t, planNote, "the attached viewer must receive a `plan` session update")
	require.Equal(t, sid, planNote.SessionID, "the projection is emitted on the OWNING unit's session")
	require.Len(t, planNote.Update.Entries, 2)
	require.Equal(t, libacp.PlanEntry{Content: "survey the codebase", Status: libacp.PlanStatusInProgress, Priority: libacp.PlanPriorityHigh}, planNote.Update.Entries[0])
	require.Equal(t, libacp.PlanEntry{Content: "port the hot loop", Status: libacp.PlanStatusPending, Priority: libacp.PlanPriorityMedium}, planNote.Update.Entries[1])
}
