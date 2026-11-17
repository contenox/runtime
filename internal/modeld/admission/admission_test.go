package admission

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type result struct {
	permit Permit
	err    error
}

func snapshot(t *testing.T, s *EventScheduler) Snapshot {
	t.Helper()
	state, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func newScheduler(t *testing.T, config Config, ids ...string) *EventScheduler {
	t.Helper()
	s := openEventFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"), config).scheduler
	for _, id := range ids {
		if err := s.Register(context.Background(), Worker{ID: id, OwnerEpoch: "epoch-1", Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func request(worker string, priority Priority) Request {
	return Request{WorkerID: worker, OwnerEpoch: "epoch-1", Priority: priority}
}

func acquire(t *testing.T, s *EventScheduler, req Request) Permit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := s.Acquire(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func release(t *testing.T, s *EventScheduler, permit Permit) {
	t.Helper()
	if err := s.Release(context.Background(), permit); err != nil {
		t.Fatal(err)
	}
}

func enqueue(s *EventScheduler, ctx context.Context, req Request) <-chan result {
	done := make(chan result, 1)
	go func() {
		permit, err := s.Acquire(ctx, req)
		done <- result{permit: permit, err: err}
	}()
	return done
}

func receive(t *testing.T, done <-chan result) result {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("acquire did not finish")
		return result{}
	}
}

func waitQueued(t *testing.T, s *EventScheduler, id string, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, w := range snapshot(t, s).Workers {
			if w.ID == id && w.QueuedForeground+w.QueuedBackground == count {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker %s did not reach queue length %d: %+v", id, count, snapshot(t, s))
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUnit_IndependentWorkersAndIdempotentRelease(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 2, ForegroundBurst: 2}, "a", "b")
	a := acquire(t, s, request("a", Foreground))
	b := acquire(t, s, request("b", Foreground))
	if got := snapshot(t, s).Active; got != 2 {
		t.Fatalf("active = %d", got)
	}
	queued := enqueue(s, context.Background(), request("a", Foreground))
	waitQueued(t, s, "a", 1)
	release(t, s, a)
	next := receive(t, queued)
	if next.err != nil || next.permit.ID == a.ID {
		t.Fatalf("next result = %+v", next)
	}
	release(t, s, a)
	if got := snapshot(t, s).Active; got != 2 {
		t.Fatalf("old release changed active count to %d", got)
	}
	release(t, s, next.permit)
	release(t, s, b)
	if err := s.Release(context.Background(), Permit{ID: 999, WorkerID: "a", OwnerEpoch: "epoch-1"}); !errors.Is(err, ErrInvalidPermit) {
		t.Fatalf("invalid permit error = %v", err)
	}
}

func TestUnit_QueueFullAndSupersession(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 2}, "a")
	active := acquire(t, s, request("a", Foreground))
	defer release(t, s, active)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := request("a", Foreground)
	old.RequesterID, old.Supersede = "editor", true
	first := enqueue(s, ctx, old)
	waitQueued(t, s, "a", 1)
	if _, err := s.Acquire(context.Background(), request("a", Background)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v", err)
	}
	second := enqueue(s, ctx, old)
	if got := receive(t, first); !errors.Is(got.err, ErrSuperseded) {
		t.Fatalf("replaced request = %+v", got)
	}
	waitQueued(t, s, "a", 1)
	if got := snapshot(t, s); got.Active != 1 || got.Workers[0].Active.ID != active.ID {
		t.Fatalf("supersession interrupted active execution: %+v", got)
	}
	cancel()
	if got := receive(t, second); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancel result = %+v", got)
	}
	waitQueued(t, s, "a", 0)
}

func TestUnit_CanceledWaitDoesNotBlockNext(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 2}, "a")
	active := acquire(t, s, request("a", Foreground))
	ctx, cancel := context.WithCancel(context.Background())
	queued := enqueue(s, ctx, request("a", Foreground))
	waitQueued(t, s, "a", 1)
	cancel()
	if got := receive(t, queued); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancel result = %+v", got)
	}
	waitQueued(t, s, "a", 0)
	next := enqueue(s, context.Background(), request("a", Foreground))
	waitQueued(t, s, "a", 1)
	release(t, s, active)
	got := receive(t, next)
	if got.err != nil {
		t.Fatal(got.err)
	}
	release(t, s, got.permit)
}

func TestUnit_DeadlineRemovesWait(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 2}, "a")
	active := acquire(t, s, request("a", Foreground))
	defer release(t, s, active)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	queued := enqueue(s, ctx, request("a", Foreground))
	if got := receive(t, queued); !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("deadline result = %+v", got)
	}
	waitQueued(t, s, "a", 0)
	if snapshot(t, s).Active != 1 {
		t.Fatal("deadline released another execution")
	}
}

func TestUnit_ForegroundPreferenceAndBoundedBackgroundWait(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 8, ForegroundBurst: 2}, "a")
	active := acquire(t, s, request("a", Background))
	background := enqueue(s, context.Background(), request("a", Background))
	waitQueued(t, s, "a", 1)
	foreground := make([]<-chan result, 3)
	for i := range foreground {
		foreground[i] = enqueue(s, context.Background(), request("a", Foreground))
		waitQueued(t, s, "a", i+2)
	}
	release(t, s, active)
	for _, done := range []<-chan result{foreground[0], foreground[1], background, foreground[2]} {
		got := receive(t, done)
		if got.err != nil {
			t.Fatal(got.err)
		}
		release(t, s, got.permit)
	}
}

func TestUnit_GlobalLimitAndCrossWorkerFairness(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 2, GlobalLimit: 1, ForegroundBurst: 1}, "a", "b", "c")
	active := acquire(t, s, request("a", Background))
	background := enqueue(s, context.Background(), request("b", Background))
	waitQueued(t, s, "b", 1)
	foreground := enqueue(s, context.Background(), request("c", Foreground))
	waitQueued(t, s, "c", 1)
	release(t, s, active)
	got := receive(t, foreground)
	if got.err != nil || got.permit.WorkerID != "c" || snapshot(t, s).Active != 1 {
		t.Fatalf("foreground grant = %+v, state = %+v", got, snapshot(t, s))
	}
	more := enqueue(s, context.Background(), request("a", Foreground))
	waitQueued(t, s, "a", 1)
	release(t, s, got.permit)
	got = receive(t, background)
	if got.err != nil {
		t.Fatal(got.err)
	}
	release(t, s, got.permit)
	got = receive(t, more)
	if got.err != nil {
		t.Fatal(got.err)
	}
	release(t, s, got.permit)
}

func TestUnit_ReadinessDrainingAndEpochFencing(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 2, ForegroundBurst: 2}, "a")
	active := acquire(t, s, request("a", Foreground))
	queued := enqueue(s, context.Background(), request("a", Foreground))
	waitQueued(t, s, "a", 1)
	if err := s.Register(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-2", Ready: true}); !errors.Is(err, ErrWorkerBusy) {
		t.Fatalf("busy replacement = %v", err)
	}
	if err := s.Update(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-1", Ready: false}); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, queued); !errors.Is(got.err, ErrNotReady) {
		t.Fatalf("not ready result = %+v", got)
	}
	if snapshot(t, s).Active != 1 {
		t.Fatal("worker failure released unknown execution")
	}
	if err := s.Update(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-1", Ready: true, Draining: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(context.Background(), request("a", Foreground)); !errors.Is(err, ErrDraining) {
		t.Fatalf("draining admission = %v", err)
	}
	release(t, s, active)
	if err := s.Register(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-2", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(context.Background(), request("a", Foreground)); !errors.Is(err, ErrOwnerEpoch) {
		t.Fatalf("old epoch admission = %v", err)
	}
	if err := s.Update(context.Background(), Worker{ID: "a", OwnerEpoch: "epoch-1", Ready: false}); !errors.Is(err, ErrOwnerEpoch) {
		t.Fatalf("old epoch update = %v", err)
	}
	req := request("a", Foreground)
	req.OwnerEpoch = "epoch-2"
	next := acquire(t, s, req)
	release(t, s, active)
	if snapshot(t, s).Active != 1 {
		t.Fatal("old epoch release changed current execution")
	}
	release(t, s, next)
}

func TestUnit_EpochReplacementInvalidatesWaitingWork(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 2, GlobalLimit: 1, ForegroundBurst: 2}, "a", "b")
	active := acquire(t, s, request("a", Foreground))
	defer release(t, s, active)
	queued := enqueue(s, context.Background(), request("b", Foreground))
	waitQueued(t, s, "b", 1)
	if err := s.Register(context.Background(), Worker{ID: "b", OwnerEpoch: "epoch-2", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, queued); !errors.Is(got.err, ErrOwnerEpoch) {
		t.Fatalf("replaced epoch result = %+v", got)
	}
	waitQueued(t, s, "b", 0)
}

func TestUnit_ConcurrentCancelAndReleaseNeverLeaks(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 2}, "a")
	for i := 0; i < 30; i++ {
		active := acquire(t, s, request("a", Foreground))
		ctx, cancel := context.WithCancel(context.Background())
		queued := enqueue(s, ctx, request("a", Foreground))
		waitQueued(t, s, "a", 1)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var releaseErr error
		go func() {
			defer wg.Done()
			<-start
			releaseErr = s.Release(context.Background(), active)
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
			release(t, s, got.permit)
		} else if !errors.Is(got.err, context.Canceled) {
			t.Fatal(got.err)
		}
		if state := snapshot(t, s); state.Active != 0 || state.Workers[0].QueuedForeground != 0 {
			t.Fatalf("iteration %d leaked state: %+v", i, state)
		}
	}
}

func TestUnit_SnapshotIsDetached(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 2}, "b", "a")
	active := acquire(t, s, request("a", Foreground))
	defer release(t, s, active)
	state := snapshot(t, s)
	if state.Workers[0].ID != "a" || state.Workers[1].ID != "b" {
		t.Fatalf("worker order = %+v", state.Workers)
	}
	state.Workers[0].Active.ID = 999
	state.Workers[0].Ready = false
	if got := snapshot(t, s); got.Workers[0].Active.ID != active.ID || !got.Workers[0].Ready {
		t.Fatalf("snapshot mutated scheduler: %+v", got)
	}
}

func TestUnit_ConfigurationAndRequestValidation(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 1}, "a")
	for _, cfg := range []Config{{}, {QueueCapacity: 1, ForegroundBurst: 1, GlobalLimit: -1}, {QueueCapacity: 1}} {
		if _, err := NewEventScheduler(context.Background(), s.db, s.bus, cfg, func(err error) { t.Errorf("admission background failure: %v", err) }); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", cfg)
		}
	}
	for _, req := range []Request{{WorkerID: "a", OwnerEpoch: "epoch-1"}, {WorkerID: "a", OwnerEpoch: "epoch-1", Priority: Foreground, Supersede: true}} {
		if _, err := s.Acquire(context.Background(), req); err == nil {
			t.Fatalf("invalid request accepted: %+v", req)
		}
	}
	if _, err := s.Acquire(context.Background(), request("missing", Foreground)); !errors.Is(err, ErrWorkerNotFound) {
		t.Fatalf("missing worker error = %v", err)
	}
	if err := s.Register(context.Background(), Worker{ID: "b"}); err == nil {
		t.Fatal("empty epoch accepted")
	}
}

func TestUnit_CanceledReleaseRetainsActiveExecution(t *testing.T) {
	s := newScheduler(t, Config{QueueCapacity: 1, ForegroundBurst: 1}, "a")
	active := acquire(t, s, request("a", Foreground))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Release(ctx, active); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled release = %v", err)
	}
	if snapshot(t, s).Active != 1 {
		t.Fatal("canceled release freed execution")
	}
	if _, err := s.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot = %v", err)
	}
	release(t, s, active)
}
