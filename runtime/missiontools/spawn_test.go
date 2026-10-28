package missiontools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type stubMissions struct{}

func (stubMissions) AddReport(context.Context, string, *missionservice.Report) error { return nil }
func (stubMissions) Heartbeat(context.Context, string, string) (*missionservice.Mission, error) {
	return nil, nil
}
func (stubMissions) SetPlan(context.Context, string, []missionservice.PlanEntry, string) (*missionservice.Mission, error) {
	return nil, nil
}
func (stubMissions) Finish(context.Context, string, missionservice.Status, string) (*missionservice.Mission, error) {
	return nil, nil
}

type fixedSpawner struct {
	calls  int
	cancel context.CancelFunc
}

func (s *fixedSpawner) Spawn(context.Context, missiontools.SubagentSpec) (missiontools.SubagentHandle, error) {
	s.calls++
	if s.cancel != nil {
		s.cancel()
	}
	return missiontools.SubagentHandle{MissionID: "m-child"}, nil
}

type fixedWatcher struct {
	mission *missionservice.Mission
	reports []*missionservice.Report
}

func (w fixedWatcher) Get(context.Context, string) (*missionservice.Mission, error) {
	return w.mission, nil
}

func (w fixedWatcher) ListReports(context.Context, string, int) ([]*missionservice.Report, error) {
	return w.reports, nil
}

func startTools(t *testing.T, spawner *fixedSpawner, watcher fixedWatcher, timeout time.Duration) taskengine.ToolsRepo {
	t.Helper()
	return missiontools.New(stubMissions{}, nil,
		missiontools.WithSpawner(spawner, watcher, func(context.Context) (string, string) { return "runner", "default" }),
		missiontools.WithSubagentTimeout(timeout),
	)
}

func startCall() *taskengine.ToolsCall {
	return &taskengine.ToolsCall{Name: missiontools.ToolsProviderName, ToolName: missiontools.ToolNameStartMission}
}

func TestUnit_MissionStartAdvertisedOnlyWhenSpawnerIsWired(t *testing.T) {
	ctx := missiontools.WithParentSessionID(context.Background(), "parent")

	plain := missiontools.New(stubMissions{}, nil,
		missiontools.WithSupervision(stubSupervisor{}, stubResolver{}))
	tools, err := plain.GetToolsForToolsByName(ctx, missiontools.ToolsProviderName)
	require.NoError(t, err)
	for _, tool := range tools {
		require.NotEqual(t, missiontools.ToolNameStartMission, tool.Function.Name)
	}

	spawner := &fixedSpawner{}
	wired := startTools(t, spawner, fixedWatcher{}, time.Minute)
	tools, err = wired.GetToolsForToolsByName(ctx, missiontools.ToolsProviderName)
	require.NoError(t, err)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}
	require.Contains(t, names, missiontools.ToolNameStartMission)
}

func TestUnit_MissionStartRequiresIntent(t *testing.T) {
	spawner := &fixedSpawner{}
	tools := startTools(t, spawner, fixedWatcher{}, time.Minute)
	_, _, err := tools.Exec(missiontools.WithParentSessionID(context.Background(), "parent"), time.Now(), map[string]any{}, false, startCall())
	require.Error(t, err)
	require.Contains(t, err.Error(), "intent")
	require.Zero(t, spawner.calls)
}

func TestUnit_MissionStartRefusesPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(missiontools.WithParentSessionID(context.Background(), "parent"))
	cancel()
	spawner := &fixedSpawner{}
	tools := startTools(t, spawner, fixedWatcher{}, time.Minute)
	_, _, err := tools.Exec(ctx, time.Now(), map[string]any{"intent": "do it"}, false, startCall())
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, spawner.calls)
}

func TestUnit_MissionStartReturnsTerminalStatusAndReports(t *testing.T) {
	spawner := &fixedSpawner{}
	watcher := fixedWatcher{
		mission: &missionservice.Mission{ID: "m-child", Status: missionservice.StatusLanded},
		reports: []*missionservice.Report{{Kind: missionservice.ReportKindResult, Summary: "done"}},
	}
	tools := startTools(t, spawner, watcher, time.Minute)
	out, kind, err := tools.Exec(missiontools.WithParentSessionID(context.Background(), "parent"), time.Now(), map[string]any{"intent": "do it"}, false, startCall())
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, kind)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.(string)), &result))
	require.Equal(t, "m-child", result["missionId"])
	require.Equal(t, "landed", result["status"])
	require.Len(t, result["reports"], 1)
	require.Equal(t, 1, spawner.calls)
}

func TestUnit_MissionStartTimeoutStillReturnsRunning(t *testing.T) {
	spawner := &fixedSpawner{}
	watcher := fixedWatcher{mission: &missionservice.Mission{ID: "m-child", Status: missionservice.StatusOpen}}
	tools := startTools(t, spawner, watcher, time.Millisecond)
	out, _, err := tools.Exec(missiontools.WithParentSessionID(context.Background(), "parent"), time.Now(), map[string]any{"intent": "do it"}, false, startCall())
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.(string)), &result))
	require.Equal(t, "running", result["status"])
	require.Contains(t, result["note"], "still running")
}

type stubSupervisor struct{}

func (stubSupervisor) MissionsFiredBy(context.Context, string, int) ([]*missionservice.Mission, error) {
	return nil, nil
}
func (stubSupervisor) ListReports(context.Context, string, int) ([]*missionservice.Report, error) {
	return nil, nil
}

type stubResolver struct{}

func (stubResolver) PendingAsks(context.Context, string) ([]missiontools.PendingAsk, error) {
	return nil, nil
}
func (stubResolver) AnswerAsAgent(context.Context, string, string) error { return nil }
