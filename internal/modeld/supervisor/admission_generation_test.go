package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

type workerAcquireResult struct {
	target  modeldconn.ModeldTarget
	release func() error
	err     error
}

func admissionWorkerSpec(role, model string) modeldconn.WorkerSpec {
	return modeldconn.WorkerSpec{Role: role, Model: model, Backend: "llama"}
}

func acquireWorker(t *testing.T, workers *modeldconn.Workers, spec modeldconn.WorkerSpec) (modeldconn.ModeldTarget, func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target, release, err := workers.Acquire(ctx, spec)
	require.NoError(t, err)
	return target, release
}

func releaseWorker(t *testing.T, release func() error) { t.Helper(); require.NoError(t, release()) }

func enqueueWorker(workers *modeldconn.Workers, ctx context.Context, spec modeldconn.WorkerSpec) <-chan workerAcquireResult {
	done := make(chan workerAcquireResult, 1)
	go func() {
		target, release, err := workers.Acquire(ctx, spec)
		done <- workerAcquireResult{target, release, err}
	}()
	return done
}

func receiveWorker(t *testing.T, done <-chan workerAcquireResult) workerAcquireResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("worker acquisition did not complete")
		return workerAcquireResult{}
	}
}

func workerAdmissionState(t *testing.T, scheduler admission.Service) admission.Snapshot {
	t.Helper()
	snapshot, err := scheduler.Snapshot(context.Background())
	require.NoError(t, err)
	return snapshot
}

func waitWorkerAdmission(t *testing.T, scheduler admission.Service, active, queued int) {
	t.Helper()
	require.Eventually(t, func() bool {
		snapshot := workerAdmissionState(t, scheduler)
		waiting := 0
		for _, worker := range snapshot.Workers {
			waiting += worker.QueuedForeground + worker.QueuedBackground
		}
		return snapshot.Active == active && waiting == queued
	}, 5*time.Second, 10*time.Millisecond)
}

func workerRefs(workers *modeldconn.Workers, model string) int {
	for _, worker := range workers.Snapshot() {
		if worker.Spec.Model == model {
			return worker.References
		}
	}
	return -1
}

func sameWorkerGeneration(t *testing.T, got, want modeldconn.ModeldTarget) bool {
	t.Helper()
	require.NotEqual(t, want.Permit, got.Permit)
	require.NotEmpty(t, got.Permit.ID)
	want.Permit = got.Permit
	require.Equal(t, want.Allocation, got.Allocation)
	want.Allocation = got.Allocation
	return got == want
}

func TestUnit_WorkersAdmission_SameWorkerSerial(t *testing.T) {
	fixture := sharedOwner(t)
	m := fixture.pool
	scheduler := m.Scheduler()
	spec := admissionWorkerSpec("primary", "model")
	first, releaseFirst := acquireWorker(t, m, spec)
	done := enqueueWorker(m, context.Background(), spec)
	waitWorkerAdmission(t, scheduler, 1, 1)
	if got := workerRefs(m, spec.Model); got != 2 {
		t.Fatalf("queued acquisition refs=%d", got)
	}
	releaseWorker(t, releaseFirst)
	next := receiveWorker(t, done)
	if next.err != nil || !sameWorkerGeneration(t, next.target, first) {
		t.Fatalf("second acquisition=%+v", next)
	}
	releaseWorker(t, releaseFirst)
	if got := workerRefs(m, spec.Model); got != 1 {
		t.Fatalf("duplicate release changed refs to %d", got)
	}
	waitWorkerAdmission(t, scheduler, 1, 0)
	releaseWorker(t, next.release)
	waitWorkerAdmission(t, scheduler, 0, 0)
	if got := workerRefs(m, spec.Model); got != 0 {
		t.Fatalf("released refs=%d", got)
	}
}

func TestUnit_WorkersAdmission_RetireConfirmsStopAndPreservesOtherManager(t *testing.T) {
	fixtureA, fixtureB := sharedOwner(t), sharedOwner(t)
	a, b := fixtureA.pool, fixtureB.pool
	scheduler := a.Scheduler()
	b.SetScheduler(scheduler)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	spec := admissionWorkerSpec("primary", "model")
	targetA, releaseA := acquireWorker(t, a, spec)
	targetB, releaseB := acquireWorker(t, b, spec)
	waitWorkerAdmission(t, scheduler, 2, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Retire(ctx, targetB); err == nil {
		t.Fatal("manager retired another runtime's worker")
	}
	waitWorkerAdmission(t, scheduler, 2, 0)
	if err := a.Retire(ctx, targetA); err != nil {
		t.Fatal(err)
	}
	state := workerAdmissionState(t, scheduler)
	if state.Active != 1 {
		t.Fatalf("confirmed retirement did not free exactly one permit: %+v", state)
	}
	for _, worker := range state.Workers {
		if worker.ID == targetA.BackendID && (!worker.Stopped || worker.Active != nil) {
			t.Fatalf("retired worker is still executable: %+v", worker)
		}
		if worker.ID == targetB.BackendID && (worker.Stopped || worker.Active == nil) {
			t.Fatalf("retirement affected another runtime: %+v", worker)
		}
	}
	inspection, err := b.InspectWorker(ctx, targetB.Instance)
	require.NoError(t, err)
	require.Equal(t, targetB.Instance, inspection.Status.OwnerInstanceID)
	releaseWorker(t, releaseA)
	waitWorkerAdmission(t, scheduler, 1, 0)
	releaseWorker(t, releaseB)
	waitWorkerAdmission(t, scheduler, 0, 0)
	next, releaseNext := acquireWorker(t, b, spec)
	if !sameWorkerGeneration(t, next, targetB) {
		t.Fatalf("other manager's worker generation changed: got=%+v want=%+v", next, targetB)
	}
	releaseWorker(t, releaseNext)
}

func TestUnit_WorkersAdmission_PreexistingQueueDrainsOldGeneration(t *testing.T) {
	fixture := sharedOwner(t)
	m := fixture.pool
	scheduler := m.Scheduler()
	oldSpec := admissionWorkerSpec("primary", "old")
	old, releaseOld := acquireWorker(t, m, oldSpec)
	done := enqueueWorker(m, context.Background(), oldSpec)
	waitWorkerAdmission(t, scheduler, 1, 1)
	newTarget, releaseNew := acquireWorker(t, m, admissionWorkerSpec("primary", "new"))
	if old.Instance == newTarget.Instance || old.BackendID == newTarget.BackendID {
		t.Fatalf("replacement reused old generation: %+v %+v", old, newTarget)
	}
	waitWorkerAdmission(t, scheduler, 2, 1)
	releaseWorker(t, releaseOld)
	next := receiveWorker(t, done)
	if next.err != nil || !sameWorkerGeneration(t, next.target, old) {
		t.Fatalf("preexisting queued generation did not drain: %+v", next)
	}
	releaseWorker(t, next.release)
	releaseWorker(t, releaseNew)
	waitWorkerAdmission(t, scheduler, 0, 0)
}
