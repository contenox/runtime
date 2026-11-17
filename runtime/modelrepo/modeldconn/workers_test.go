package modeldconn

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/stretchr/testify/require"
)

func fakeWorker(ctx context.Context, spec WorkerSpec, root string) (workerHandle, error) {
	done := make(chan struct{})
	var once sync.Once
	stop := func() error { once.Do(func() { close(done) }); return nil }
	return workerHandle{status: modeldprobe.Status{State: modeldprobe.StateRunning, Endpoint: root, Instance: root}, done: done, stop: stop}, nil
}

func TestUnit_Workers_DataRootIsRuntimeScoped(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	ctxA := WithDataRoot(context.Background(), rootA)
	ctxB := WithDataRoot(context.Background(), rootB)
	require.Equal(t, rootA, DataRootFromContext(ctxA))
	require.Equal(t, rootB, DataRootFromContext(ctxB))
	m := NewWorkers(context.Background(), rootB)
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	require.Equal(t, rootB, DataRootFromContext(WithWorkers(ctxA, m)))
	require.Equal(t, rootA, DataRootFromContext(ctxA))
}

func TestUnit_WorkerOptions_ResourceControls(t *testing.T) {
	for _, valid := range []WorkerOptions{
		{Headroom: "auto", ComputeReserve: "auto", MinHotContext: "auto"},
		{Headroom: "0%", ComputeReserve: "25%", MinHotContext: "0"},
		{Headroom: "5%", ComputeReserve: "256MiB", MinHotContext: "2048"},
	} {
		require.NoError(t, valid.Validate())
	}
	for _, invalid := range []WorkerOptions{
		{Headroom: "2GiB"}, {Headroom: "101%"}, {ComputeReserve: "garbage"}, {MinHotContext: "-1"},
	} {
		require.Error(t, invalid.Validate())
	}
	ctx := WithAdmissionOptions(context.Background(), AdmissionOptions{Priority: admission.Background})
	require.Equal(t, admission.Background, admissionOptions(ctx).Priority)
	require.Equal(t, admission.Foreground, admissionOptions(context.Background()).Priority)
}

func TestUnit_WorkerLaunchUsesBackendFlag(t *testing.T) {
	spec := WorkerSpec{Backend: "openvino", Memory: "8GiB"}
	args := workerServeArgs(spec, "/worker", "/models")
	require.Equal(t, []string{"serve", "--data-root", "/worker", "--models-dir", "/models", "--backend", "openvino", "--mem-max", "8GiB"}, args)

	env := workerEnvironment([]string{"PATH=/bin", "CONTENOX_DATA_ROOT=/old"}, "/worker")
	require.Equal(t, []string{"PATH=/bin", "CONTENOX_DATA_ROOT=/worker"}, env)
}

func TestUnit_Workers_IndependentStartupAndCoalescing(t *testing.T) {
	m := NewWorkers(context.Background(), t.TempDir())
	defer func() { require.NoError(t, m.Close()) }()
	blocked := make(chan struct{})
	started := make(chan struct{})
	var launches atomic.Int32
	m.launch = func(ctx context.Context, spec WorkerSpec, root string) (workerHandle, error) {
		if spec.Role == "primary" {
			launches.Add(1)
			close(started)
			select {
			case <-blocked:
			case <-ctx.Done():
				return workerHandle{}, ctx.Err()
			}
		}
		return fakeWorker(ctx, spec, root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	primary := WorkerSpec{Role: "primary", Model: "large", Backend: "llama"}
	errCh := make(chan error, 1)
	go func() {
		_, release, err := m.Acquire(ctx, primary)
		if err == nil {
			release()
		}
		errCh <- err
	}()
	<-started
	target, release, err := m.Acquire(ctx, WorkerSpec{Role: "autocomplete", Model: "small", Backend: "openvino"})
	require.NoError(t, err)
	require.NotEmpty(t, target.Endpoint)
	release()
	waitCtx, stop := context.WithCancel(ctx)
	stop()
	_, _, err = m.Acquire(waitCtx, primary)
	require.ErrorIs(t, err, context.Canceled)
	close(blocked)
	require.NoError(t, <-errCh)
	_, release, err = m.Acquire(ctx, primary)
	require.NoError(t, err)
	release()
	require.EqualValues(t, 1, launches.Load())
}

func TestUnit_Workers_ConfigurationChangeDrainsPrevious(t *testing.T) {
	m := NewWorkers(context.Background(), t.TempDir())
	m.launch = fakeWorker
	defer func() { require.NoError(t, m.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	spec := WorkerSpec{Role: "primary", Model: "a", Backend: "llama"}
	a, releaseA, err := m.Acquire(ctx, spec)
	require.NoError(t, err)
	spec.Model = "b"
	b, releaseB, err := m.Acquire(ctx, spec)
	require.NoError(t, err)
	defer releaseB()
	require.NotEqual(t, a.Endpoint, b.Endpoint)
	m.mu.Lock()
	var old *workerEntry
	for _, entry := range m.entries {
		if entry.spec.Model == "a" {
			old = entry
		}
	}
	m.mu.Unlock()
	require.NotNil(t, old)
	select {
	case <-old.handle.done:
		t.Fatal("active worker stopped")
	default:
	}
	releaseA()
	select {
	case <-old.handle.done:
	case <-ctx.Done():
		t.Fatal("draining worker not stopped")
	}
}

func TestUnit_Workers_CloseOwnsOnlyItsProcesses(t *testing.T) {
	a := NewWorkers(context.Background(), t.TempDir())
	b := NewWorkers(context.Background(), t.TempDir())
	a.launch = fakeWorker
	b.launch = fakeWorker
	defer func() { require.NoError(t, b.Close()) }()
	spec := WorkerSpec{Role: "embedding", Model: "embed", Backend: "llama"}
	_, releaseA, err := a.Acquire(context.Background(), spec)
	require.NoError(t, err)
	defer releaseA()
	bTarget, releaseB, err := b.Acquire(context.Background(), spec)
	require.NoError(t, err)
	defer releaseB()
	require.NoError(t, a.Close())
	_, _, err = a.Acquire(context.Background(), spec)
	require.Error(t, err)
	again, release, err := b.Acquire(context.Background(), spec)
	require.NoError(t, err)
	defer release()
	require.Equal(t, bTarget, again)
}
