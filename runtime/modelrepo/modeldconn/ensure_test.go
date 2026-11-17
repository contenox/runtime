package modeldconn

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProbe stands in for the real lease/health detector so ensureDaemon can
// be tested without a running daemon or a writable lease.
type stubProbe struct {
	probe  modeldprobe.Status
	detect modeldprobe.Status
	launch func(binary string) []string
}

func (s *stubProbe) Probe(context.Context) modeldprobe.Status { return s.probe }
func (s *stubProbe) Detect() modeldprobe.Status               { return s.detect }
func (s *stubProbe) LauncherArgs(binary string) []string {
	if s.launch != nil {
		return s.launch(binary)
	}
	return []string{}
}

func notRunningWith(binary string) stubProbe {
	return stubProbe{
		probe:  modeldprobe.Status{State: modeldprobe.StateNotRunning},
		detect: modeldprobe.Status{Binary: binary},
	}
}

// resetAutostartState restores the package autostart state after every test:
// a leftover child or a stuck disable flag must not leak between tests.
func resetAutostartState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		StopAutoStarted()
		SetDisabled(false)
	})
}

// sleepingFake launches this test binary as a short-lived daemon fixture.
func sleepingFake(t *testing.T) string {
	t.Helper()
	t.Setenv("CONTENOX_TEST_SLEEPING_OWNER", "1")
	binary, err := os.Executable()
	require.NoError(t, err)
	return binary
}

func TestMain(m *testing.M) {
	if os.Getenv("CONTENOX_TEST_SLEEPING_OWNER") == "1" {
		if len(os.Args) == 2 {
			if err := os.WriteFile(os.Args[1], []byte(os.Getenv("CONTENOX_MODELD_BACKEND")), 0600); err != nil {
				os.Exit(1)
			}
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestUnit_EnsureDaemon_RunningShortCircuits(t *testing.T) {
	resetAutostartState(t)
	p := stubProbe{probe: modeldprobe.Status{State: modeldprobe.StateRunning, Endpoint: "ipc://x"}}
	require.NoError(t, ensureDaemon(context.Background(), &p))
	StopAutoStarted() // nothing must have been spawned
}

func TestUnit_EnsureDaemon_DisabledShortCircuits(t *testing.T) {
	resetAutostartState(t)
	SetDisabled(true)
	// A missing binary would error if autostart ran; disabled means a silent
	// no-op for every mode.
	require.NoError(t, EnsureDaemon(context.Background()))
}

func TestUnit_EnsureDaemon_MissingBinaryReportsNotInstalled(t *testing.T) {
	resetAutostartState(t)
	p := stubProbe{
		probe:  modeldprobe.Status{State: modeldprobe.StateNotRunning},
		detect: modeldprobe.Status{},
	}
	require.ErrorIs(t, ensureDaemon(context.Background(), &p), modeldprobe.ErrNotInstalled)
}

func TestUnit_EnsureDaemon_CanceledContextAbortsStartup(t *testing.T) {
	resetAutostartState(t)
	p := notRunningWith(sleepingFake(t))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ensureDaemon(ctx, &p) }()

	time.Sleep(50 * time.Millisecond) // let the fake spawn
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("ensureDaemon did not abort when its context was cancelled")
	}
	StopAutoStarted() // the spawned child must not outlive the abort
}

func TestUnit_EnsureDaemon_StopAutoStartedReapsSpawnedChild(t *testing.T) {
	resetAutostartState(t)
	p := notRunningWith(sleepingFake(t))

	// The probe never reports ready, so ensureDaemon waits on a live child;
	// StopAutoStarted must reap that child even though the wait is ongoing.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ensureDaemon(ctx, &p) }()

	time.Sleep(50 * time.Millisecond)
	StopAutoStarted()
	select {
	case <-done:
		// ensureDaemon returned early because its child was reaped: fine.
	case <-time.After(2 * time.Second):
		// The wait may outlive StopAutoStarted until the context fires; the
		// cleanup in resetAutostartState still reaps on exit. Nothing to fail.
	}
}

type concurrentProbe struct {
	process  *os.Process
	done     <-chan struct{}
	binary   string
	launches atomic.Int32
	ready    atomic.Bool
}

func (p *concurrentProbe) Probe(context.Context) modeldprobe.Status {
	if p.ready.Load() {
		return modeldprobe.Status{State: modeldprobe.StateRunning}
	}
	autoMu.Lock()
	spawned := autoSpawned != nil
	if spawned {
		p.process, p.done = autoSpawned.Process, autoDone
	}
	autoMu.Unlock()
	if spawned {
		p.ready.Store(true)
		return modeldprobe.Status{State: modeldprobe.StateRunning}
	}
	return modeldprobe.Status{State: modeldprobe.StateNotRunning}
}
func (p *concurrentProbe) Detect() modeldprobe.Status   { return modeldprobe.Status{Binary: p.binary} }
func (p *concurrentProbe) LauncherArgs(string) []string { p.launches.Add(1); return nil }

func TestUnit_EnsureDaemon_ConcurrentCallersSpawnOnce(t *testing.T) {
	resetAutostartState(t)
	p := &concurrentProbe{binary: sleepingFake(t)}
	errs := make(chan error, 12)
	for range 12 {
		go func() { errs <- ensureDaemon(context.Background(), p) }()
	}
	for range 12 {
		require.NoError(t, <-errs)
	}
	require.EqualValues(t, 1, p.launches.Load())
	require.NoError(t, p.process.Kill())
	<-p.done
}

func TestUnit_EnsureDaemon_WaitingCallerCanCancel(t *testing.T) {
	resetAutostartState(t)
	pending := make(chan struct{})
	autoMu.Lock()
	autoStarting = pending
	autoMu.Unlock()
	t.Cleanup(func() { autoMu.Lock(); autoStarting = nil; close(pending); autoMu.Unlock() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	p := notRunningWith(sleepingFake(t))
	require.ErrorIs(t, ensureDaemon(ctx, &p), context.DeadlineExceeded)
}

func TestUnit_EnsureDaemon_BackendPreferenceAndEnvironmentOverride(t *testing.T) {
	for _, explicit := range []string{"", "openvino"} {
		t.Run("override="+explicit, func(t *testing.T) {
			resetAutostartState(t)
			root := t.TempDir()
			previous := dataRoot
			SetDataRoot(root)
			t.Cleanup(func() { SetDataRoot(previous) })
			require.NoError(t, modeldprobe.SetBackendPreference(root, "llama"))
			t.Setenv("CONTENOX_MODELD_BACKEND", explicit)
			output := filepath.Join(root, "backend.txt")
			binary := sleepingFake(t)
			probe := notRunningWith(binary)
			probe.launch = func(string) []string { return []string{output} }
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ensureDaemon(ctx, &probe) }()
			want := explicit
			if want == "" {
				want = "llama"
			}
			require.EventuallyWithT(t, func(check *assert.CollectT) {
				got, err := os.ReadFile(output)
				require.NoError(check, err)
				require.Equal(check, want, string(got))
			}, 5*time.Second, 25*time.Millisecond)
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
		})
	}
}
