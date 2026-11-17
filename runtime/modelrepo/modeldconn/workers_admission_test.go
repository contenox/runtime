package modeldconn

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type workerAcquireResult struct {
	target  ModeldTarget
	release func() error
	err     error
}

func newAdmissionWorkers(t *testing.T) (*Workers, *admission.EventScheduler) {
	t.Helper()
	db, err := libdbexec.NewSQLiteDBManager(context.Background(), filepath.Join(t.TempDir(), "runtime.sqlite"), runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatal(err)
	}
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() {
		if err := errors.Join(bus.Close(), db.Close()); err != nil {
			t.Error(err)
		}
	})
	scheduler, err := admission.NewEventScheduler(context.Background(), db, bus, admission.Config{QueueCapacity: 4, ForegroundBurst: 2}, func(err error) {
		t.Errorf("admission background failure: %v", err)
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewWorkers(context.Background(), t.TempDir())
	m.launch = fakeWorker
	m.SetScheduler(scheduler)
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m, scheduler
}

func admissionWorkerSpec(role, model string) WorkerSpec {
	return WorkerSpec{Role: role, Model: model, Backend: "llama"}
}

func acquireWorker(t *testing.T, m *Workers, spec WorkerSpec) (ModeldTarget, func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, release, err := m.Acquire(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	return target, release
}

func releaseWorker(t *testing.T, release func() error) {
	t.Helper()
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func enqueueWorker(m *Workers, ctx context.Context, spec WorkerSpec) <-chan workerAcquireResult {
	done := make(chan workerAcquireResult, 1)
	go func() {
		target, release, err := m.Acquire(ctx, spec)
		done <- workerAcquireResult{target: target, release: release, err: err}
	}()
	return done
}

func receiveWorker(t *testing.T, done <-chan workerAcquireResult) workerAcquireResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("worker acquisition did not complete")
		return workerAcquireResult{}
	}
}

func workerAdmissionState(t *testing.T, scheduler *admission.EventScheduler) admission.Snapshot {
	t.Helper()
	state, err := scheduler.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func waitWorkerAdmission(t *testing.T, scheduler *admission.EventScheduler, active, queued int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := workerAdmissionState(t, scheduler)
		waiting := 0
		for _, worker := range state.Workers {
			waiting += worker.QueuedForeground + worker.QueuedBackground
		}
		if state.Active == active && waiting == queued {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active=%d queued=%d, got %+v", active, queued, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func workerRefs(m *Workers, model string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.entries {
		if entry.spec.Model == model {
			return entry.refs
		}
	}
	return -1
}

func TestUnit_WorkersAdmission_DifferentRolesIndependent(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	primary, releasePrimary := acquireWorker(t, m, admissionWorkerSpec("primary", "large"))
	autocomplete, releaseAutocomplete := acquireWorker(t, m, admissionWorkerSpec("autocomplete", "small"))
	if primary.Instance == autocomplete.Instance || primary.BackendID == autocomplete.BackendID {
		t.Fatalf("worker identities collided: %+v %+v", primary, autocomplete)
	}
	waitWorkerAdmission(t, scheduler, 2, 0)
	releaseWorker(t, releasePrimary)
	waitWorkerAdmission(t, scheduler, 1, 0)
	releaseWorker(t, releaseAutocomplete)
	waitWorkerAdmission(t, scheduler, 0, 0)
}

func TestUnit_WorkersAdmission_QueuedCancellationDropsReference(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	spec := admissionWorkerSpec("primary", "model")
	_, releaseActive := acquireWorker(t, m, spec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enqueueWorker(m, ctx, spec)
	waitWorkerAdmission(t, scheduler, 1, 1)
	cancel()
	got := receiveWorker(t, done)
	if !errors.Is(got.err, context.Canceled) || got.release != nil || got.target != (ModeldTarget{}) {
		t.Fatalf("canceled acquisition=%+v", got)
	}
	if refs := workerRefs(m, spec.Model); refs != 1 {
		t.Fatalf("cancellation left refs=%d", refs)
	}
	waitWorkerAdmission(t, scheduler, 1, 0)
	releaseWorker(t, releaseActive)
}

func TestUnit_WorkersAdmission_RuntimeCloseCancelsQueuedRequest(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	spec := admissionWorkerSpec("primary", "model")
	_, releaseActive := acquireWorker(t, m, spec)
	done := enqueueWorker(m, context.Background(), spec)
	waitWorkerAdmission(t, scheduler, 1, 1)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	got := receiveWorker(t, done)
	if got.err == nil || got.release != nil || got.target != (ModeldTarget{}) {
		t.Fatalf("closed manager returned worker=%+v", got)
	}
	if refs := workerRefs(m, spec.Model); refs != 1 {
		t.Fatalf("close cancellation left refs=%d", refs)
	}
	waitWorkerAdmission(t, scheduler, 0, 0)
	releaseWorker(t, releaseActive)
	waitWorkerAdmission(t, scheduler, 0, 0)
}

type failingReleaseScheduler struct {
	admission.Service
	err   error
	calls atomic.Int32
}

func (s *failingReleaseScheduler) Release(context.Context, admission.Permit) error {
	s.calls.Add(1)
	return s.err
}

func TestUnit_WorkersAdmission_ReleaseFailureReachesCaller(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	releaseFailure := errors.New("admission release failed")
	wrapper := &failingReleaseScheduler{Service: scheduler, err: releaseFailure}
	m.SetScheduler(wrapper)
	_, release := acquireWorker(t, m, admissionWorkerSpec("primary", "model"))
	if err := release(); !errors.Is(err, releaseFailure) {
		t.Fatalf("release failure=%v", err)
	}
	if wrapper.calls.Load() != 1 {
		t.Fatalf("release calls=%d", wrapper.calls.Load())
	}
	if got := workerRefs(m, "model"); got != 0 {
		t.Fatalf("failed admission release leaked process ref=%d", got)
	}
	state := workerAdmissionState(t, scheduler)
	if state.Active != 1 {
		t.Fatalf("failed release freed permit: %+v", state)
	}
	if err := scheduler.Release(context.Background(), *state.Workers[0].Active); err != nil {
		t.Fatal(err)
	}
}

type closeOnlySession struct {
	transport.Session
	err   error
	calls atomic.Int32
}

func (s *closeOnlySession) Close() error {
	s.calls.Add(1)
	return s.err
}

func TestUnit_WorkerSession_ReportsAdmissionReleaseFailure(t *testing.T) {
	releaseFailure := errors.New("admission release failed")
	session := &closeOnlySession{}
	var releases atomic.Int32
	wrapped := &workerSession{Session: session, release: func() error {
		releases.Add(1)
		return releaseFailure
	}}
	for i := 0; i < 2; i++ {
		if err := wrapped.Close(); !errors.Is(err, releaseFailure) {
			t.Fatalf("close failure=%v", err)
		}
	}
	if session.calls.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("close calls=%d release calls=%d", session.calls.Load(), releases.Load())
	}
}

func TestUnit_WorkerSession_FailedCloseRetainsPermit(t *testing.T) {
	closeFailure := errors.New("session close could not confirm execution stopped")
	session := &closeOnlySession{err: closeFailure}
	var releases atomic.Int32
	wrapped := &workerSession{Session: session, release: func() error {
		releases.Add(1)
		return nil
	}}
	if err := wrapped.Close(); !errors.Is(err, closeFailure) {
		t.Fatalf("session close failure=%v", err)
	}
	if releases.Load() != 0 {
		t.Fatal("uncertain execution was released after session close failure")
	}
}

type grantGateScheduler struct {
	admission.Service
	granted chan struct{}
	resume  chan struct{}
}

func (s *grantGateScheduler) Acquire(ctx context.Context, request admission.Request) (admission.Permit, error) {
	permit, err := s.Service.Acquire(ctx, request)
	if err != nil {
		return permit, err
	}
	close(s.granted)
	<-s.resume
	return permit, nil
}

func TestUnit_WorkersAdmission_CloseAfterGrantRejectsNewExecution(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	gate := &grantGateScheduler{Service: scheduler, granted: make(chan struct{}), resume: make(chan struct{})}
	m.SetScheduler(gate)
	done := enqueueWorker(m, context.Background(), admissionWorkerSpec("primary", "model"))
	select {
	case <-gate.granted:
	case <-time.After(5 * time.Second):
		t.Fatal("permit was not granted")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	close(gate.resume)
	got := receiveWorker(t, done)
	if got.err == nil || got.release != nil || got.target != (ModeldTarget{}) {
		t.Fatalf("closed manager returned granted execution=%+v", got)
	}
	waitWorkerAdmission(t, scheduler, 0, 0)
	if refs := workerRefs(m, "model"); refs != 0 {
		t.Fatalf("rejected grant leaked refs=%d", refs)
	}
}

func TestUnit_WorkersAdmission_ExitedWorkerRejectsQueue(t *testing.T) {
	m, scheduler := newAdmissionWorkers(t)
	spec := admissionWorkerSpec("primary", "model")
	_, releaseActive := acquireWorker(t, m, spec)
	done := enqueueWorker(m, context.Background(), spec)
	waitWorkerAdmission(t, scheduler, 1, 1)
	m.mu.Lock()
	var stop func() error
	for _, entry := range m.entries {
		if entry.spec.Model == spec.Model {
			stop = entry.handle.stop
		}
	}
	m.mu.Unlock()
	if stop == nil {
		t.Fatal("missing worker process")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	got := receiveWorker(t, done)
	if got.err == nil || got.release != nil || got.target != (ModeldTarget{}) {
		t.Fatalf("exited worker returned queued execution=%+v", got)
	}
	waitWorkerAdmission(t, scheduler, 0, 0)
	releaseWorker(t, releaseActive)
	waitWorkerAdmission(t, scheduler, 0, 0)
	m.mu.Lock()
	for _, entry := range m.entries {
		entry.err = nil
	}
	m.mu.Unlock()
}
