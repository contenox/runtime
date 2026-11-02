package fleetservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/stretchr/testify/require"
)

func TestFleetService_Dispatch_FailedTurnDerailsAndStopsUnit(t *testing.T) {
	ctx, db := setupRegistryDB(t)
	agents := agentregistryservice.New(db)
	registerAgent(t, ctx, agents, "runner", true)
	missions := missionservice.New(db)

	man := &fakeManager{startID: "inst-err", openID: "sess-err", promptErr: errors.New(`template fallback var "provider" is not set`)}
	svc := New(man, agents, missions, nil, "/project/root", libtracker.NoopTracker{})

	res, err := svc.Dispatch(ctx, DispatchRequest{AgentName: "runner", Intent: "review the diff", HITLPolicyName: "default"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		m, gerr := missions.Get(ctx, res.MissionID)
		return gerr == nil && m.Status == missionservice.StatusDerailed && len(man.stops()) == 1
	}, 5*time.Second, 20*time.Millisecond)

	m, err := missions.Get(ctx, res.MissionID)
	require.NoError(t, err)
	require.Contains(t, m.StatusReason, `template fallback var "provider" is not set`)
	require.Nil(t, m.LastHeartbeat)

	reps, err := missions.ListReports(ctx, res.MissionID, 5)
	require.NoError(t, err)
	require.Len(t, reps, 1)
	require.Equal(t, missionservice.ReportKindBlocker, reps[0].Kind)
	require.Contains(t, reps[0].Summary, turnErrorBlockerLead)
	require.Contains(t, reps[0].Detail, "sess-err")

	require.Equal(t, 1, man.prompts())
	require.Equal(t, []string{"inst-err"}, man.stops())
}

func TestFleetService_Dispatch_FailedNudgeTurnDerails(t *testing.T) {
	ctx, db := setupRegistryDB(t)
	agents := agentregistryservice.New(db)
	registerAgent(t, ctx, agents, "runner", true)
	missions := missionservice.New(db)

	man := &fakeManager{startID: "inst-n", openID: "sess-n"}
	man.onPrompt = func(call int) {
		if call == 2 {
			man.mu.Lock()
			man.promptErr = errors.New("backend unreachable")
			man.mu.Unlock()
		}
	}
	svc := New(man, agents, missions, nil, "/project/root", libtracker.NoopTracker{})

	res, err := svc.Dispatch(ctx, DispatchRequest{AgentName: "runner", Intent: "review", HITLPolicyName: "default"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		m, gerr := missions.Get(ctx, res.MissionID)
		return gerr == nil && m.Status == missionservice.StatusDerailed && len(man.stops()) == 1
	}, 5*time.Second, 20*time.Millisecond)
	reps, err := missions.ListReports(ctx, res.MissionID, 5)
	require.NoError(t, err)
	require.Len(t, reps, 1)
	require.Contains(t, reps[0].Summary, "backend unreachable")
	require.Equal(t, 2, man.prompts())
}

func TestFleetService_Stop_AbandonsTheUnitsOpenMission(t *testing.T) {
	ctx, db := setupRegistryDB(t)
	agents := agentregistryservice.New(db)
	missions := missionservice.New(db)

	m := &missionservice.Mission{Intent: "review", AgentName: "runner", HITLPolicyName: "default"}
	require.NoError(t, missions.Create(ctx, m))
	_, err := missions.Bind(ctx, m.ID, "sess-s", "inst-s")
	require.NoError(t, err)

	man := &fakeManager{}
	svc := New(man, agents, missions, nil, "", libtracker.NoopTracker{})

	require.NoError(t, svc.Stop(ctx, "inst-s"))
	got, err := missions.Get(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, missionservice.StatusAbandoned, got.Status)
	require.Equal(t, StoppedByOperatorReason, got.StatusReason)

	require.NoError(t, svc.Stop(ctx, "inst-s"))
	got, err = missions.Get(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, missionservice.StatusAbandoned, got.Status)
	require.Equal(t, []string{"inst-s", "inst-s"}, man.stops())
}

func TestFleetService_Stop_LeavesAFinishedMissionAlone(t *testing.T) {
	ctx, db := setupRegistryDB(t)
	agents := agentregistryservice.New(db)
	missions := missionservice.New(db)

	m := &missionservice.Mission{Intent: "review", AgentName: "runner", HITLPolicyName: "default"}
	require.NoError(t, missions.Create(ctx, m))
	_, err := missions.Bind(ctx, m.ID, "sess-l", "inst-l")
	require.NoError(t, err)
	_, err = missions.Finish(ctx, m.ID, missionservice.StatusLanded, "done")
	require.NoError(t, err)

	man := &fakeManager{}
	svc := New(man, agents, missions, nil, "", libtracker.NoopTracker{})
	require.NoError(t, svc.Stop(ctx, "inst-l"))

	got, err := missions.Get(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, missionservice.StatusLanded, got.Status)
	require.Equal(t, []string{"inst-l"}, man.stops())
}

func TestFleetService_Dispatch_RecordsCwdResolvedAgentAndIntentMeta(t *testing.T) {
	ctx, db := setupRegistryDB(t)
	agents := agentregistryservice.New(db)
	registerAgent(t, ctx, agents, "runner", true)
	missions := missionservice.New(db)

	man := &fakeManager{startID: "inst-c", openID: "sess-c"}
	man.onPrompt = func(call int) {
		if ms, _ := missions.List(context.Background(), nil, 10); len(ms) == 1 {
			_ = missions.AddReport(context.Background(), ms[0].ID, &missionservice.Report{Kind: missionservice.ReportKindResult, Summary: "ok"})
		}
	}
	svc := New(man, agents, missions, nil, "/project/root", libtracker.NoopTracker{})

	res, err := svc.Dispatch(ctx, DispatchRequest{AgentName: "  runner ", Intent: "review the working tree", HITLPolicyName: "default"})
	require.NoError(t, err)
	waitMissionSettled(t, missions, res.MissionID)

	m, err := missions.Get(ctx, res.MissionID)
	require.NoError(t, err)
	require.Equal(t, "/project/root", m.Cwd)
	require.Equal(t, "runner", m.AgentName)

	require.Len(t, man.openSpecs, 1)
	meta, ok := missionservice.ParseMissionMetaFull(man.openSpecs[0].Meta)
	require.True(t, ok)
	require.Equal(t, res.MissionID, meta.MissionID)
	require.Equal(t, "review the working tree", meta.Intent)
}
