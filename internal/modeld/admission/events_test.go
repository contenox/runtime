package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type eventFixture struct {
	scheduler *EventScheduler
	db        libdbexec.DBManager
	bus       libbus.Messenger
}

func openEventFixture(t *testing.T, path string, config Config) eventFixture {
	t.Helper()
	db, err := libdbexec.NewSQLiteDBManager(context.Background(), path, runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatal(err)
	}
	bus := libbus.NewSQLiteWithOptions(db.WithoutTransaction(), libbus.SQLiteBusOptions{EventPoll: 5 * time.Millisecond, RequestPoll: 5 * time.Millisecond})
	t.Cleanup(func() {
		if err := errors.Join(bus.Close(), db.Close()); err != nil {
			t.Error(err)
		}
	})
	s, err := NewEventScheduler(context.Background(), db, bus, config, func(err error) {
		t.Errorf("admission background failure: %v", err)
	})
	if err != nil {
		t.Fatal(err)
	}
	return eventFixture{scheduler: s, db: db, bus: bus}
}

func registerEventWorker(t *testing.T, s *EventScheduler, id string) {
	t.Helper()
	if err := s.Register(context.Background(), Worker{ID: id, OwnerEpoch: "epoch-1", Ready: true}); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_EventScheduler_ConfigureSharesBoundsWithoutPreemptingAcceptedWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	initial := Config{QueueCapacity: 4, ForegroundBurst: 2, MaxQueueWait: 30 * time.Second}
	a := openEventFixture(t, path, initial)
	b := openEventFixture(t, path, initial)
	for _, id := range []string{"a", "b", "c"} {
		registerEventWorker(t, a.scheduler, id)
	}
	activeA := acquire(t, a.scheduler, request("a", Foreground))
	activeB := acquire(t, b.scheduler, request("b", Foreground))
	updated := Config{QueueCapacity: 1, GlobalLimit: 1, ForegroundBurst: 1, MaxQueueWait: 20 * time.Second}
	if err := a.scheduler.Configure(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	for _, scheduler := range []*EventScheduler{a.scheduler, b.scheduler} {
		state := snapshot(t, scheduler)
		if state.Policy != updated || state.GlobalLimit != 1 || state.Active != 2 {
			t.Fatalf("policy update preempted work or stayed connection-local: %+v", state)
		}
		if state.Workers[0].Active == nil || *state.Workers[0].Active != activeA || state.Workers[1].Active == nil || *state.Workers[1].Active != activeB {
			t.Fatalf("accepted permits changed during policy update: %+v", state)
		}
	}
	queuedA := enqueue(b.scheduler, context.Background(), request("a", Foreground))
	waitQueued(t, a.scheduler, "a", 1)
	if _, err := b.scheduler.Acquire(context.Background(), request("a", Background)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("new queue bound was ignored by preexisting connection: %v", err)
	}
	queuedC := enqueue(a.scheduler, context.Background(), request("c", Foreground))
	waitQueued(t, b.scheduler, "c", 1)
	release(t, b.scheduler, activeA)
	state := snapshot(t, a.scheduler)
	if state.Active != 1 || state.Workers[0].QueuedForeground != 1 || state.Workers[2].QueuedForeground != 1 || state.Workers[1].Active == nil || *state.Workers[1].Active != activeB {
		t.Fatalf("new global cap allowed replacement before accepted work drained: %+v", state)
	}
	release(t, a.scheduler, activeB)
	got := receive(t, queuedA)
	if got.err != nil || got.permit.WorkerID != "a" {
		t.Fatalf("first queued grant=%+v", got)
	}
	if snapshot(t, b.scheduler).Active != 1 {
		t.Fatal("updated global cap was exceeded")
	}
	release(t, a.scheduler, got.permit)
	got = receive(t, queuedC)
	if got.err != nil || got.permit.WorkerID != "c" {
		t.Fatalf("remaining queued grant=%+v", got)
	}
	release(t, b.scheduler, got.permit)
}

func TestUnit_EventScheduler_ConfigurePersistsCanonicalKVAndReplaysCurrentPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	initial := Config{QueueCapacity: 4, ForegroundBurst: 2}
	a := openEventFixture(t, path, initial)
	b := openEventFixture(t, path, initial)
	registerEventWorker(t, a.scheduler, "a")
	var checkpoint string
	if err := a.db.WithoutTransaction().QueryRowContext(context.Background(), `SELECT state FROM modeld_admission WHERE id=1`).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	updated := Config{QueueCapacity: 2, GlobalLimit: 1, ForegroundBurst: 3, MaxQueueWait: 45 * time.Second}
	if err := b.scheduler.Configure(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := runtimetypes.New(a.db.WithoutTransaction()).GetKV(context.Background(), clikv.Prefix+"inference.modeld.admission", &raw); err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil || persisted != updated {
		t.Fatalf("canonical admission policy=%+v err=%v", persisted, err)
	}
	active := acquire(t, a.scheduler, request("a", Foreground))
	expected := snapshot(t, b.scheduler)
	if _, err := a.db.WithoutTransaction().ExecContext(context.Background(), `UPDATE modeld_admission SET state=$1 WHERE id=1`, checkpoint); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, b.scheduler); !reflect.DeepEqual(got, expected) || got.Policy != updated {
		t.Fatalf("stale checkpoint replay changed current policy: got=%+v want=%+v", got, expected)
	}
	reopened := openEventFixture(t, path, updated)
	if state := snapshot(t, reopened.scheduler); state.Policy != updated || state.Active != 1 {
		t.Fatalf("reopened scheduler lost configured policy or active work: %+v", state)
	}
	release(t, reopened.scheduler, active)
}

func TestUnit_EventScheduler_ConfigureFailureRollsBackEventPolicyAndCanonicalKV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	initial := Config{QueueCapacity: 4, ForegroundBurst: 2}
	a := openEventFixture(t, path, initial)
	b := openEventFixture(t, path, initial)
	if err := a.scheduler.Configure(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	store := runtimetypes.NewEventStore(b.db)
	before, err := store.ListEventsSince(context.Background(), admissionWorkspace, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("policy checkpoint write failed")
	a.scheduler.db = projectionFailureDB{DBManager: a.db, failure: failure}
	updated := Config{QueueCapacity: 1, GlobalLimit: 1, ForegroundBurst: 1, MaxQueueWait: time.Minute}
	if err := a.scheduler.Configure(context.Background(), updated); !errors.Is(err, failure) {
		t.Fatalf("configure failure=%v", err)
	}
	if state := snapshot(t, b.scheduler); state.Policy != initial {
		t.Fatalf("failed configuration changed shared policy: %+v", state)
	}
	var raw string
	if err := runtimetypes.New(b.db.WithoutTransaction()).GetKV(context.Background(), clikv.Prefix+"inference.modeld.admission", &raw); err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil || persisted != initial {
		t.Fatalf("failed configuration changed canonical policy: %+v err=%v", persisted, err)
	}
	after, err := store.ListEventsSince(context.Background(), admissionWorkspace, 0, 100)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed configuration retained policy event: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestUnit_EventScheduler_ExplicitQueueLimitDoesNotExpireAcceptedExecution(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 2, ForegroundBurst: 2, MaxQueueWait: 50 * time.Millisecond}, "a")
	active := acquire(t, s, request("a", Foreground))
	_, err := s.Acquire(context.Background(), request("a", Foreground))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue limit result: %v", err)
	}
	state := snapshot(t, s)
	if state.Active != 1 || state.Workers[0].QueuedForeground != 0 {
		t.Fatalf("queue timeout changed accepted execution: %+v", state)
	}
	release(t, s, active)
}

func TestUnit_EventScheduler_ConcurrentDrainWorkersShareClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	a := openEventFixture(t, path, config).scheduler
	b := openEventFixture(t, path, config).scheduler
	registerEventWorker(t, a, "a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { done <- a.Run(ctx) }()
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
	active := acquire(t, a, request("a", Foreground))
	// Apply the queue deadline only after the first worker has its permit.
	config.MaxQueueWait = 50 * time.Millisecond
	if err := a.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	_, err := b.Acquire(context.Background(), request("a", Foreground))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued request result: %v", err)
	}
	if snapshot(t, b).Active != 1 {
		t.Fatal("concurrent drain lost accepted permit")
	}
	release(t, b, active)
	if snapshot(t, a).Active != 0 {
		t.Fatal("release not visible across workers")
	}
}

func TestUnit_EventScheduler_IndependentConnectionsSerializeSameWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 12, ForegroundBurst: 2}
	owner := openEventFixture(t, path, config)
	registerEventWorker(t, owner.scheduler, "a")
	active := acquire(t, owner.scheduler, request("a", Foreground))
	const connections = 8
	clients := make([]eventFixture, connections)
	for i := range clients {
		clients[i] = openEventFixture(t, path, config)
	}
	// This bounds the entire eight-connection test, including serial SQLite
	// writes. Queue expiration is exercised separately.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	results := make(chan result, connections)
	for _, client := range clients {
		go func() {
			permit, err := client.scheduler.Acquire(ctx, request("a", Foreground))
			results <- result{permit: permit, err: err}
		}()
	}
	waitQueued(t, owner.scheduler, "a", connections)
	release(t, owner.scheduler, active)
	seen := map[uint64]bool{active.ID: true}
	for range connections {
		got := receive(t, results)
		if got.err != nil || seen[got.permit.ID] {
			t.Fatalf("independent connection permit=%+v, error=%v, duplicate=%v", got.permit, got.err, seen[got.permit.ID])
		}
		seen[got.permit.ID] = true
		if state := snapshot(t, owner.scheduler); state.Active != 1 || state.Workers[0].Active == nil || *state.Workers[0].Active != got.permit {
			t.Fatalf("same worker admitted overlapping execution: %+v", state)
		}
		release(t, owner.scheduler, got.permit)
	}
	if snapshot(t, clients[0].scheduler).Active != 0 {
		t.Fatal("independent connection retained released execution")
	}
}

func TestUnit_EventScheduler_IndependentConnectionsHonorGlobalCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, GlobalLimit: 1, ForegroundBurst: 2}
	a := openEventFixture(t, path, config)
	b := openEventFixture(t, path, config)
	registerEventWorker(t, a.scheduler, "a")
	registerEventWorker(t, b.scheduler, "b")
	active := acquire(t, a.scheduler, request("a", Background))
	queued := enqueue(b.scheduler, context.Background(), request("b", Foreground))
	waitQueued(t, a.scheduler, "b", 1)
	if snapshot(t, b.scheduler).Active != 1 {
		t.Fatal("global cap was bypassed across connections")
	}
	release(t, a.scheduler, active)
	got := receive(t, queued)
	if got.err != nil || got.permit.WorkerID != "b" {
		t.Fatalf("cross-connection grant=%+v", got)
	}
	release(t, a.scheduler, got.permit)
	if snapshot(t, b.scheduler).Active != 0 {
		t.Fatal("release on another connection did not persist")
	}
}

func TestUnit_EventScheduler_IndependentConnectionsRunDifferentWorkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	a := openEventFixture(t, path, config)
	b := openEventFixture(t, path, config)
	registerEventWorker(t, a.scheduler, "a")
	registerEventWorker(t, b.scheduler, "b")
	p := acquire(t, a.scheduler, request("a", Foreground))
	q := acquire(t, b.scheduler, request("b", Foreground))
	if state := snapshot(t, a.scheduler); state.Active != 2 {
		t.Fatalf("independent workers did not overlap: %+v", state)
	}
	release(t, b.scheduler, p)
	release(t, a.scheduler, q)
}

func TestUnit_EventScheduler_ActivePermitSurvivesRestartAndDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	first := openEventFixture(t, path, config)
	registerEventWorker(t, first.scheduler, "a")
	ctx, cancel := context.WithCancel(context.Background())
	permit, err := first.scheduler.Acquire(ctx, request("a", Foreground))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	var raw string
	if err := first.db.WithoutTransaction().QueryRowContext(context.Background(), `SELECT state FROM modeld_admission WHERE id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state admissionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	for id, entry := range state.Requests {
		entry.Deadline = time.Now().Add(-time.Hour).UnixNano()
		state.Requests[id] = entry
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.WithoutTransaction().ExecContext(context.Background(), `UPDATE modeld_admission SET state=$1 WHERE id=1`, string(encoded)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(first.bus.Close(), first.db.Close()); err != nil {
		t.Fatal(err)
	}
	restarted := openEventFixture(t, path, config)
	if state := snapshot(t, restarted.scheduler); state.Active != 1 || state.Workers[0].Active == nil || *state.Workers[0].Active != permit {
		t.Fatalf("restart/deadline reclaimed accepted execution: %+v", state)
	}
	if err := restarted.scheduler.Register(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-2", Ready: true}); !errors.Is(err, ErrWorkerBusy) {
		t.Fatalf("restart allowed uncertain owner replacement: %v", err)
	}
	release(t, restarted.scheduler, permit)
	next := acquire(t, restarted.scheduler, request("a", Foreground))
	if next.ID <= permit.ID {
		t.Fatalf("permit IDs reset across restart: old=%+v new=%+v", permit, next)
	}
	release(t, restarted.scheduler, permit)
	if snapshot(t, restarted.scheduler).Active != 1 {
		t.Fatal("stale pre-restart release freed newer execution")
	}
	release(t, restarted.scheduler, next)
}

func TestUnit_EventScheduler_ReplaysStaleAndEmptyCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	f := openEventFixture(t, path, config)
	registerEventWorker(t, f.scheduler, "a")
	old := acquire(t, f.scheduler, request("a", Foreground))
	var stale string
	if err := f.db.WithoutTransaction().QueryRowContext(context.Background(), `SELECT state FROM modeld_admission WHERE id=1`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	release(t, f.scheduler, old)
	current := acquire(t, f.scheduler, request("a", Background))
	expected := snapshot(t, f.scheduler)
	for _, checkpoint := range []string{stale, `{"Cursor":0,"Workers":{},"Requests":{},"Burst":0}`} {
		if _, err := f.db.WithoutTransaction().ExecContext(context.Background(), `UPDATE modeld_admission SET state=$1 WHERE id=1`, checkpoint); err != nil {
			t.Fatal(err)
		}
		reader := openEventFixture(t, path, config)
		if got := snapshot(t, reader.scheduler); !reflect.DeepEqual(got, expected) {
			t.Fatalf("replay disagrees with committed projection: got=%+v want=%+v", got, expected)
		}
	}
	release(t, f.scheduler, current)
}

type projectionFailureDB struct {
	libdbexec.DBManager
	failure error
	commit  bool
}

type projectionFailureExec struct {
	libdbexec.Exec
	failure error
}

func (e projectionFailureExec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(query, "UPDATE modeld_admission SET state=") {
		return nil, e.failure
	}
	return e.Exec.ExecContext(ctx, query, args...)
}

func (d projectionFailureDB) WithTransaction(ctx context.Context, callbacks ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	exec, commit, release, err := d.DBManager.WithTransaction(ctx, callbacks...)
	if err != nil {
		return exec, commit, release, err
	}
	if d.commit {
		return exec, func(context.Context) error { return d.failure }, release, nil
	}
	return projectionFailureExec{Exec: exec, failure: d.failure}, commit, release, nil
}

func TestUnit_EventScheduler_EventAndProjectionRollBackTogether(t *testing.T) {
	for _, failCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "projection_write", true: "commit"}[failCommit], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.sqlite")
			config := Config{QueueCapacity: 2, ForegroundBurst: 2}
			f := openEventFixture(t, path, config)
			registerEventWorker(t, f.scheduler, "a")
			store := runtimetypes.NewEventStore(f.db)
			before, err := store.ListEventsSince(context.Background(), admissionWorkspace, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("projection transaction interrupted")
			f.scheduler.db = projectionFailureDB{DBManager: f.db, failure: failure, commit: failCommit}
			if err := f.scheduler.Register(context.Background(), Worker{ID: "b", OwnerEpoch: "epoch-1", Ready: true}); !errors.Is(err, failure) {
				t.Fatalf("transaction failure=%v", err)
			}
			after, err := store.ListEventsSince(context.Background(), admissionWorkspace, 0, 100)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed projection retained event: before=%+v after=%+v err=%v", before, after, err)
			}
			reader := openEventFixture(t, path, config)
			if got := snapshot(t, reader.scheduler); len(got.Workers) != 1 || got.Workers[0].ID != "a" {
				t.Fatalf("failed transaction retained projection: %+v", got)
			}
		})
	}
}

func TestUnit_EventScheduler_CancellationAndReleaseAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	a := openEventFixture(t, path, config)
	b := openEventFixture(t, path, config)
	registerEventWorker(t, a.scheduler, "a")
	for i := 0; i < 30; i++ {
		active := acquire(t, a.scheduler, request("a", Foreground))
		ctx, cancel := context.WithCancel(context.Background())
		queued := enqueue(b.scheduler, ctx, request("a", Foreground))
		waitQueued(t, a.scheduler, "a", 1)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var releaseErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			releaseErr = a.scheduler.Release(context.Background(), active)
		}()
		go func() {
			defer wg.Done()
			<-start
			cancel()
		}()
		close(start)
		wg.Wait()
		if releaseErr != nil {
			t.Fatal(releaseErr)
		}
		got := receive(t, queued)
		if got.err == nil {
			release(t, a.scheduler, got.permit)
		} else if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cross-connection cancellation=%v", got.err)
		}
		if state := snapshot(t, b.scheduler); state.Active != 0 || state.Workers[0].QueuedForeground != 0 {
			t.Fatalf("race iteration %d leaked state: %+v", i, state)
		}
	}
}

type cancelAcceptedBus struct {
	libbus.Messenger
	db     libdbexec.DBManager
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelAcceptedBus) Publish(ctx context.Context, subject string, data []byte) error {
	var raw string
	if err := b.db.WithoutTransaction().QueryRowContext(ctx, `SELECT state FROM modeld_admission WHERE id=1`).Scan(&raw); err != nil {
		return err
	}
	var state admissionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	for _, entry := range state.Requests {
		if entry.Accepted {
			b.once.Do(b.cancel)
		}
	}
	return b.Messenger.Publish(ctx, subject, data)
}

func TestUnit_EventScheduler_CancelAfterAcceptedCommitBeforeReturnReclaimsPermit(t *testing.T) {
	f := openEventFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"), Config{QueueCapacity: 2, ForegroundBurst: 2})
	registerEventWorker(t, f.scheduler, "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.scheduler.bus = &cancelAcceptedBus{Messenger: f.bus, db: f.db, cancel: cancel}
	if permit, err := f.scheduler.Acquire(ctx, request("a", Foreground)); !errors.Is(err, context.Canceled) || permit.ID != 0 {
		t.Fatalf("cancellation after accepted commit=%+v err=%v", permit, err)
	}
	if state := snapshot(t, f.scheduler); state.Active != 0 || state.Workers[0].QueuedForeground != 0 {
		t.Fatalf("unreturned accepted grant leaked: %+v", state)
	}
}

type failingWakeBus struct {
	libbus.Messenger
	failure error
}

func (b failingWakeBus) Publish(context.Context, string, []byte) error { return b.failure }

func TestUnit_EventScheduler_FailedWakeupsAreReportedAndPollingProgresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	config := Config{QueueCapacity: 2, ForegroundBurst: 2}
	f := openEventFixture(t, path, config)
	wakeFailure := errors.New("wakeup publish failed")
	var mu sync.Mutex
	var reports []error
	s, err := NewEventScheduler(context.Background(), f.db, failingWakeBus{Messenger: f.bus, failure: wakeFailure}, config, func(err error) {
		mu.Lock()
		reports = append(reports, err)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	registerEventWorker(t, s, "a")
	active := acquire(t, s, request("a", Foreground))
	done := enqueue(s, context.Background(), request("a", Foreground))
	waitQueued(t, s, "a", 1)
	release(t, s, active)
	got := receive(t, done)
	if got.err != nil {
		t.Fatalf("missing wakeup blocked polling progress: %v", got.err)
	}
	release(t, s, got.permit)
	if snapshot(t, f.scheduler).Active != 0 {
		t.Fatal("failed bus publish changed durable release")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) == 0 {
		t.Fatal("wakeup failures were dropped")
	}
	for _, report := range reports {
		if !errors.Is(report, wakeFailure) {
			t.Fatalf("unexpected failure report: %v", report)
		}
	}
}
