package workspacetriggers

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	ctx     context.Context
	db      libdb.DBManager
	factory *vfs.Factory
	log     eventlog.Service
	events  workspaceevents.Service
	store   Store
	history History
	root    string
	scope   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "triggers.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	root, err := vfs.ResolveRoot(t.TempDir())
	require.NoError(t, err)
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	log := eventlog.NewService(db, nil, libtracker.NoopTracker{})
	events := workspaceevents.New("ws-trg", log, bus, factory, libtracker.NoopTracker{})
	store := Store{DB: db}
	return fixture{
		ctx: ctx, db: db, factory: factory, log: log, events: events, store: store,
		history: History{DB: db, Log: log, Store: store},
		root:    root, scope: workspaceevents.Scope("ws-trg", root),
	}
}

func (f fixture) trigger(t *testing.T, mutate func(*Trigger)) Trigger {
	t.Helper()
	tr := Trigger{
		Root:           f.root,
		Name:           "tests changed",
		Events:         []string{"fs.modified", "fs.created"},
		PathGlob:       "**/*_test.go",
		DebounceMs:     300,
		AgentName:      "reviewer",
		HITLPolicyName: "read_only",
		Prompt:         "Run the tests that changed and report.",
		Enabled:        true,
	}
	if mutate != nil {
		mutate(&tr)
	}
	require.NoError(t, f.store.Create(f.ctx, f.scope, &tr))
	return tr
}

func (f fixture) disk(t *testing.T, ctx context.Context, typ, path string) {
	t.Helper()
	_, err := f.events.Append(ctx, f.root, workspaceevents.Input{Type: typ, Source: workspaceevents.SourceWatcher, Subject: path})
	require.NoError(t, err)
}

func (f fixture) firings(t *testing.T) []Firing {
	t.Helper()
	list, err := f.history.Firings(f.ctx, f.scope, 50)
	require.NoError(t, err)
	return list
}

type fakeFleet struct {
	mu     sync.Mutex
	runs   []Run
	status map[string]string
	refuse error
}

func newFakeFleet() *fakeFleet { return &fakeFleet{status: map[string]string{}} }

func (f *fakeFleet) StartMission(_ context.Context, run Run) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		return "", f.refuse
	}
	f.runs = append(f.runs, run)
	id := "mission-" + string(rune('a'+len(f.runs)-1))
	f.status[id] = "open"
	return id, nil
}

func (f *fakeFleet) state(_ context.Context, id string) (MissionOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.status[id]
	if !ok {
		return MissionOutcome{}, errors.New("no such mission")
	}
	return MissionOutcome{Terminal: s != "open", OK: s == "landed", Status: s}, nil
}

func (f *fakeFleet) finish(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = status
}

func (f *fakeFleet) started() []Run {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Run(nil), f.runs...)
}

func (f fixture) config(fleet *fakeFleet, events workspaceevents.Service) DispatcherConfig {
	return DispatcherConfig{
		WorkspaceID: "ws-trg",
		DB:          f.db,
		Log:         f.log,
		Events:      events,
		Roots:       f.factory,
		Store:       f.store,
		Starter:     fleet,
		Missions:    fleet.state,
		Reconcile:   50 * time.Millisecond,
		Tick:        20 * time.Millisecond,
		RunCheck:    20 * time.Millisecond,
		Grace:       300 * time.Millisecond,
	}
}

func (f fixture) dispatcher(t *testing.T, fleet *fakeFleet) *Dispatcher {
	t.Helper()
	d := NewDispatcher(f.config(fleet, f.events))
	d.Start(f.ctx)
	t.Cleanup(d.Stop)
	waitActive(t, d, f.root)
	return d
}

func waitActive(t *testing.T, d *Dispatcher, root string) {
	t.Helper()
	require.Eventually(t, func() bool { return d.Active(root) }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
}

func TestUnit_Store_ValidatesAndKeepsNamesUnique(t *testing.T) {
	f := newFixture(t)
	first := f.trigger(t, nil)
	require.Equal(t, 300, first.DebounceMs)
	require.False(t, first.CreatedAt.IsZero())

	dup := Trigger{Name: "Tests Changed", Events: []string{"fs.modified"}, AgentName: "a", HITLPolicyName: "p", Prompt: "x"}
	require.ErrorIs(t, f.store.Create(f.ctx, f.scope, &dup), ErrNameTaken)

	for name, bad := range map[string]Trigger{
		"no agent":    {Name: "n", Events: []string{"fs.modified"}, HITLPolicyName: "p", Prompt: "x"},
		"no envelope": {Name: "n", Events: []string{"fs.modified"}, AgentName: "a", Prompt: "x"},
		"no prompt":   {Name: "n", Events: []string{"fs.modified"}, AgentName: "a", HITLPolicyName: "p"},
		"tool event":  {Name: "n", Events: []string{"tool.wrote"}, AgentName: "a", HITLPolicyName: "p", Prompt: "x"},
		"no events":   {Name: "n", AgentName: "a", HITLPolicyName: "p", Prompt: "x"},
		"fast":        {Name: "n", Events: []string{"fs.modified"}, AgentName: "a", HITLPolicyName: "p", Prompt: "x", DebounceMs: 10},
		"two lines":   {Name: "n", Events: []string{"fs.modified"}, AgentName: "a", HITLPolicyName: "p", Prompt: "x\ny"},
		"absolute":    {Name: "n", Events: []string{"fs.modified"}, AgentName: "a", HITLPolicyName: "p", Prompt: "x", PathGlob: "/etc/*"},
	} {
		require.ErrorIs(t, f.store.Create(f.ctx, f.scope, &bad), ErrInvalidTrigger, name)
	}

	first.Name = "renamed"
	first.Enabled = false
	require.NoError(t, f.store.Update(f.ctx, f.scope, &first))
	got, err := f.store.Get(f.ctx, f.scope, first.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
	require.False(t, got.Enabled)
	require.Equal(t, first.CreatedAt.UTC(), got.CreatedAt.UTC(), "an update keeps when the trigger was made")
	require.Equal(t, f.root, got.Root)
	require.Equal(t, []string{"fs.created", "fs.modified"}, got.Events)

	other := workspaceevents.Scope("ws-trg", t.TempDir())
	list, err := f.store.List(f.ctx, other)
	require.NoError(t, err)
	require.Empty(t, list, "triggers belong to one root")

	require.NoError(t, f.store.Delete(f.ctx, f.scope, first.ID))
	require.ErrorIs(t, f.store.Delete(f.ctx, f.scope, first.ID), ErrNotFound)
	_, err = f.store.Get(f.ctx, f.scope, first.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIntegration_Dispatcher_OneMissionPerBurstAfterTheDebounce(t *testing.T) {
	f := newFixture(t)
	tr := f.trigger(t, nil)
	fleet := newFakeFleet()
	f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "pkg/a_test.go")
	f.disk(t, f.ctx, "fs.modified", "pkg/a.go")
	f.disk(t, f.ctx, "fs.created", "pkg/b_test.go")
	f.disk(t, f.ctx, "fs.modified", "pkg/a_test.go")
	f.disk(t, f.ctx, "fs.deleted", "pkg/c_test.go")
	time.Sleep(150 * time.Millisecond)
	require.Empty(t, fleet.started(), "the debounce holds the burst")

	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)
	run := fleet.started()[0]
	require.Equal(t, tr.ID, run.Trigger.ID)
	require.Equal(t, f.root, run.Root)
	require.Equal(t, []string{"pkg/a_test.go", "pkg/b_test.go"}, run.Paths)
	require.Equal(t, `Run the tests that changed and report. (Started by the workspace trigger "tests changed" after file changes on: "pkg/a_test.go", "pkg/b_test.go".)`, run.Intent())

	time.Sleep(500 * time.Millisecond)
	require.Len(t, fleet.started(), 1)
	firings := f.firings(t)
	require.Len(t, firings, 1)
	require.Equal(t, runtimetypes.EventFiringStatusRunning, firings[0].Status)
	require.Equal(t, "mission-a", firings[0].MissionID, "the mission is found again through its trigger.started event")
	require.Equal(t, "tests changed", firings[0].TriggerName)
}

func TestIntegration_History_NamesFiringsAfterTheTriggerIsRenamedOrDeleted(t *testing.T) {
	f := newFixture(t)
	tr := f.trigger(t, nil)
	fleet := newFakeFleet()
	f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "pkg/a_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { rows := f.firings(t); return len(rows) == 1 && rows[0].MissionID != "" }, 3*time.Second, 20*time.Millisecond)

	tr.Name = "renamed"
	require.NoError(t, f.store.Update(f.ctx, f.scope, &tr))
	require.Equal(t, "renamed", f.firings(t)[0].TriggerName, "a live trigger is shown by its current name")

	require.NoError(t, f.store.Delete(f.ctx, f.scope, tr.ID))
	firings := f.firings(t)
	require.Len(t, firings, 1)
	require.Equal(t, tr.ID, firings[0].TriggerID)
	require.Equal(t, "tests changed", firings[0].TriggerName, "a deleted trigger keeps the name it fired under")
	require.Equal(t, "mission-a", firings[0].MissionID)
}

func TestIntegration_Dispatcher_NeverFiresOnWhatItsOwnMissionDoes(t *testing.T) {
	f := newFixture(t)
	f.trigger(t, nil)
	fleet := newFakeFleet()
	f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "a_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)

	f.disk(t, f.ctx, "fs.modified", "a_test.go")
	f.disk(t, f.ctx, "fs.created", "new_test.go")
	time.Sleep(600 * time.Millisecond)
	require.Len(t, fleet.started(), 1, "writes while the mission runs are its own")

	fleet.finish("mission-a", "landed")
	require.Eventually(t, func() bool {
		firings := f.firings(t)
		return len(firings) == 1 && firings[0].Status == runtimetypes.EventFiringStatusOK
	}, 3*time.Second, 20*time.Millisecond)
	f.disk(t, f.ctx, "fs.modified", "a_test.go")
	time.Sleep(500 * time.Millisecond)
	require.Len(t, fleet.started(), 1, "the grace period covers the last writes the watcher reports late")

	time.Sleep(200 * time.Millisecond)
	f.disk(t, f.ctx, "fs.modified", "a_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 2 }, 3*time.Second, 20*time.Millisecond)

	fleet.finish("mission-b", "derailed")
	require.Eventually(t, func() bool {
		for _, fr := range f.firings(t) {
			if fr.MissionID == "mission-b" {
				return fr.Status == runtimetypes.EventFiringStatusError && fr.Error == "mission derailed"
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)
	time.Sleep(700 * time.Millisecond)
	f.disk(t, runtimetypes.WithEventHop(f.ctx, DefaultMaxHop+1), "fs.modified", "hop_test.go")
	require.Eventually(t, func() bool {
		for _, fr := range f.firings(t) {
			if fr.Status == runtimetypes.EventFiringStatusRefused {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "an event past the hop limit is refused and recorded")
	require.Len(t, fleet.started(), 2)
}

func TestIntegration_Dispatcher_ATriggerCreatedJustBeforeAnEditSeesIt(t *testing.T) {
	f := newFixture(t)
	fleet := newFakeFleet()
	f.dispatcher(t, fleet)

	f.trigger(t, nil)
	f.disk(t, f.ctx, "fs.modified", "fresh_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)
}

func TestIntegration_Dispatcher_ResumesFromItsCursorAndIgnoresWhatCameBeforeTheTrigger(t *testing.T) {
	f := newFixture(t)
	f.disk(t, f.ctx, "fs.modified", "before_test.go")
	time.Sleep(20 * time.Millisecond)
	f.trigger(t, nil)

	fleet := newFakeFleet()
	d := f.dispatcher(t, fleet)
	time.Sleep(500 * time.Millisecond)
	require.Empty(t, fleet.started(), "an edit from before the trigger existed never fires it")

	d.Stop()
	f.disk(t, f.ctx, "fs.modified", "while_down_test.go")
	f.dispatcher(t, fleet)
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"while_down_test.go"}, fleet.started()[0].Paths, "a restart catches up from the cursor")
}

func TestIntegration_Dispatcher_ARestartDuringTheDebounceKeepsTheBurst(t *testing.T) {
	f := newFixture(t)
	f.trigger(t, func(tr *Trigger) { tr.DebounceMs = 1500 })
	fleet := newFakeFleet()
	d := f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "a_test.go")
	f.disk(t, f.ctx, "fs.modified", "b_test.go")
	time.Sleep(300 * time.Millisecond)
	d.Stop()
	require.Empty(t, fleet.started())

	f.dispatcher(t, fleet)
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 5*time.Second, 20*time.Millisecond,
		"the open burst stayed behind the cursor, so the restarted dispatcher still fires it")
	require.Equal(t, []string{"a_test.go", "b_test.go"}, fleet.started()[0].Paths)
}

func TestIntegration_Dispatcher_TwoDispatchersStartOneMission(t *testing.T) {
	f := newFixture(t)
	f.trigger(t, nil)
	fleet := newFakeFleet()
	f.dispatcher(t, fleet)
	f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "shared_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond)
	time.Sleep(600 * time.Millisecond)
	require.Len(t, fleet.started(), 1, "the claim on (trigger, event) lets one dispatcher win")
	require.Len(t, f.firings(t), 1)
}

func TestIntegration_Dispatcher_AStartFailureIsRecordedAndDisabledTriggersStayQuiet(t *testing.T) {
	f := newFixture(t)
	f.trigger(t, nil)
	f.trigger(t, func(tr *Trigger) {
		tr.Name = "off"
		tr.Enabled = false
	})
	fleet := newFakeFleet()
	fleet.refuse = errors.New("hitl policy \"read_only\" could not be loaded")
	f.dispatcher(t, fleet)

	f.disk(t, f.ctx, "fs.modified", "x_test.go")
	require.Eventually(t, func() bool {
		firings := f.firings(t)
		return len(firings) == 1 && firings[0].Status == runtimetypes.EventFiringStatusError
	}, 3*time.Second, 20*time.Millisecond)
	firings := f.firings(t)
	require.Contains(t, firings[0].Error, "could not be loaded")
	require.Equal(t, "tests changed", firings[0].TriggerName, "one firing, the disabled trigger stayed quiet")
}

type flakySince struct {
	workspaceevents.Service
	mu       sync.Mutex
	failures int
}

func (f *flakySince) Since(ctx context.Context, root string, after int64, limit int) ([]workspaceevents.Event, error) {
	f.mu.Lock()
	fail := f.failures > 0
	if fail {
		f.failures--
	}
	f.mu.Unlock()
	if fail {
		return nil, errors.New("database is locked")
	}
	return f.Service.Since(ctx, root, after, limit)
}

func TestIntegration_Dispatcher_AWorkerRecoversAfterAnError(t *testing.T) {
	f := newFixture(t)
	f.trigger(t, nil)
	fleet := newFakeFleet()
	events := &flakySince{Service: f.events, failures: 2}
	d := NewDispatcher(f.config(fleet, events))
	d.Start(f.ctx)
	t.Cleanup(d.Stop)
	waitActive(t, d, f.root)

	require.Eventually(t, func() bool {
		events.mu.Lock()
		defer events.mu.Unlock()
		return events.failures == 0
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	f.disk(t, f.ctx, "fs.modified", "after_error_test.go")
	require.Eventually(t, func() bool { return len(fleet.started()) == 1 }, 3*time.Second, 20*time.Millisecond,
		"a failed pass is retried on the next tick")
}
func TestUnit_ScheduledTopicCatchesUpOnceAndStaysInProject(t *testing.T) {
	f := newFixture(t)
	schedules := workspaceevents.Schedules{DB: f.db}
	now := time.Now().UTC().Truncate(time.Millisecond)
	stored, err := schedules.Create(f.ctx, f.scope, workspaceevents.Schedule{Topic: "review.due", Every: "1s"}, now)
	require.NoError(t, err)
	due := now.Add(10 * time.Second)
	published, err := schedules.Drain(f.ctx, f.scope, due)
	require.NoError(t, err)
	require.Len(t, published, 1)
	require.Equal(t, workspaceevents.SourceSchedule, published[0].Source)
	published, err = schedules.Drain(f.ctx, f.scope, due)
	require.NoError(t, err)
	require.Empty(t, published)
	rows, err := f.events.Recent(f.ctx, f.root, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "review.due", rows[0].Subject)
	disabled := false
	require.NoError(t, schedules.Change(f.ctx, f.scope, stored.ID, &disabled))
	published, err = schedules.Drain(f.ctx, f.scope, due.Add(time.Minute))
	require.NoError(t, err)
	require.Empty(t, published)
	require.Error(t, schedules.Change(f.ctx, "another-root", stored.ID, nil))
}

func TestIntegration_FiringPagesPreserveSiblingTriggersAcrossCursors(t *testing.T) {
	f := newFixture(t)
	store, err := f.history.firingStore(f.scope)
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c"} {
		ok, err := store.BeginEventFiring(f.ctx, id, 22, "request-"+id)
		require.NoError(t, err)
		require.True(t, ok)
	}
	p, c, err := f.history.Page(f.ctx, f.scope, "", 2, 22, false)
	require.NoError(t, err)
	require.Len(t, p, 2)
	require.Equal(t, "22/b", c)
	p, c, err = f.history.Page(f.ctx, f.scope, c, 2, 22, false)
	require.NoError(t, err)
	require.Len(t, p, 1)
	require.Equal(t, "c", p[0].TriggerID)
	require.Empty(t, c)
	waiting, err := f.history.WaitingPage(f.ctx, f.scope, "", 1)
	require.NoError(t, err)
	require.Len(t, waiting.Active, 1)
	require.Equal(t, "22/a", waiting.NextCursor)
}

func TestIntegration_WaitingShowsAnOpenMissionBlocker(t *testing.T) {
	f := newFixture(t)
	rule := f.trigger(t, nil)
	missions := missionservice.New(f.db)
	m := &missionservice.Mission{Intent: "Review the change.", HITLPolicyName: "read_only"}
	require.NoError(t, missions.Create(f.ctx, m))
	store, err := f.history.firingStore(f.scope)
	require.NoError(t, err)
	id := RequestID(rule.ID, 17)
	ok, err := store.BeginEventFiring(f.ctx, rule.ID, 17, id)
	require.NoError(t, err)
	require.True(t, ok)
	data, err := json.Marshal(map[string]string{"missionId": m.ID, "triggerName": rule.Name})
	require.NoError(t, err)
	_, err = f.events.Append(f.ctx, f.root, workspaceevents.Input{Type: workspaceevents.TypeTriggerStarted, Source: workspaceevents.SourceTrigger, Subject: id, Data: data})
	require.NoError(t, err)
	require.NoError(t, missions.AddReport(f.ctx, m.ID, &missionservice.Report{Kind: missionservice.ReportKindBlocker, Summary: "The reviewer needs input."}))
	waiting, err := f.history.Waiting(f.ctx, f.scope)
	require.NoError(t, err)
	require.Empty(t, waiting.Active)
	require.Len(t, waiting.Parked, 1)
	require.Equal(t, "The reviewer needs input.", waiting.Parked[0].Error)
	require.Equal(t, runtimetypes.EventFiringStatusRunning, f.firings(t)[0].Status)
}
