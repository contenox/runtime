package libsandbox_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/libtracker"
	"github.com/stretchr/testify/require"
)

type fakeTracker struct {
	starts, ends, changes int
	errs                  []error
}

func (f *fakeTracker) Start(ctx context.Context, operation, subject string, kvArgs ...any) (func(error), func(string, any), func()) {
	f.starts++
	return func(err error) { f.errs = append(f.errs, err) },
		func(string, any) { f.changes++ },
		func() { f.ends++ }
}

var _ libtracker.ActivityTracker = (*fakeTracker)(nil)

func TestUnit_Command_AssemblesMinimalValidSpec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Command assembles a runnable command only on Linux; off Linux it fails closed")
	}
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "shhh")

	cmd, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: ws,
		Home:          home,
		EnvAllow:      []string{"PATH"},
	}, "true", "arg1")

	require.NoError(t, err)
	require.NotNil(t, cmd)
	require.Equal(t, ws, cmd.Dir)
	require.Equal(t, []string{"true", "arg1"}, cmd.Args)
	require.Contains(t, cmd.Env, "PATH=/usr/bin")
	require.Contains(t, cmd.Env, "HOME="+home)
	for _, kv := range cmd.Env {
		require.NotContains(t, kv, "AWS_SECRET_ACCESS_KEY")
	}
}

func TestUnit_Command_ConfinedPathKeepsCarvedToolchainDropsUncarved(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Command assembles a runnable command only on Linux")
	}
	t.Setenv("PATH", "/opt/toolchain/node/bin:/usr/bin:/home/dev/.cargo/bin")

	cmd, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: t.TempDir(),
		Home:          t.TempDir(),
		FS:            []libsandbox.FSCarveout{{Path: "/opt/toolchain", Mode: "ro", Needs: "node runtime the agent execs"}},
	}, "true")

	require.NoError(t, err)
	require.Contains(t, cmd.Env, "PATH=/opt/toolchain/node/bin:/usr/bin")
}

func TestUnit_Command_RejectsPathOutsideExecSurface(t *testing.T) {
	_, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: "/ws",
		Home:          "/h",
		EnvSet:        map[string]string{"PATH": "/opt/rogue/bin"},
	}, "true")

	require.ErrorIs(t, err, libsandbox.ErrInvalidSpec)
}

func TestUnit_Command_RejectsRelativePathEntry(t *testing.T) {
	_, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: "/ws",
		Home:          "/h",
		EnvSet:        map[string]string{"PATH": "/usr/bin:relative/bin"},
	}, "true")

	require.ErrorIs(t, err, libsandbox.ErrInvalidSpec)
}

func TestUnit_Command_AllowsPathWithinCarveout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a successful Command assembly is only reachable on Linux")
	}
	cmd, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: t.TempDir(),
		Home:          t.TempDir(),
		EnvSet:        map[string]string{"PATH": "/opt/tools/bin"},
		FS:            []libsandbox.FSCarveout{{Path: "/opt/tools", Mode: "ro", Needs: "toolchain on PATH"}},
	}, "true")

	require.NoError(t, err)
	require.Contains(t, cmd.Env, "PATH=/opt/tools/bin")
}

func TestUnit_Command_RejectsEmptyWorkspace(t *testing.T) {
	_, err := libsandbox.Command(context.Background(),
		libsandbox.Spec{Home: "/h"}, "true")

	require.ErrorIs(t, err, libsandbox.ErrInvalidSpec)
}

func TestUnit_Command_RejectsEmptyHome(t *testing.T) {
	_, err := libsandbox.Command(context.Background(),
		libsandbox.Spec{WorkspaceRoot: "/ws"}, "true")

	require.ErrorIs(t, err, libsandbox.ErrInvalidSpec)
}

func TestUnit_Command_RejectsEmptyName(t *testing.T) {
	_, err := libsandbox.Command(context.Background(),
		libsandbox.Spec{WorkspaceRoot: "/ws", Home: "/h"}, "")

	require.ErrorIs(t, err, libsandbox.ErrInvalidSpec)
}

func TestUnit_Command_RejectsInvalidCarveout(t *testing.T) {
	_, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: "/ws",
		Home:          "/h",
		FS:            []libsandbox.FSCarveout{{Path: "../etc", Mode: "ro", Needs: "x"}},
	}, "true")

	require.ErrorIs(t, err, libsandbox.ErrInvalidCarveout)
}

func TestUnit_Command_EmitsTrackerLifecycleOnSuccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a successful Command assembly is only reachable on Linux")
	}
	tr := &fakeTracker{}

	_, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: t.TempDir(),
		Home:          t.TempDir(),
		Tracker:       tr,
	}, "true")

	require.NoError(t, err)
	require.Equal(t, 1, tr.starts)
	require.Equal(t, 1, tr.changes)
	require.Equal(t, 1, tr.ends)
	require.Empty(t, tr.errs)
}

func TestUnit_Command_ReportsErrorToTracker(t *testing.T) {
	tr := &fakeTracker{}

	_, err := libsandbox.Command(context.Background(),
		libsandbox.Spec{Home: "/h", Tracker: tr}, "true")

	require.Error(t, err)
	require.Len(t, tr.errs, 1)
	require.Equal(t, 0, tr.changes)
	require.Equal(t, 1, tr.ends)
}

func TestUnit_Command_FailsClosedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("on Linux the wall is built; the off-Linux fail-closed path does not apply")
	}
	cmd, err := libsandbox.Command(context.Background(), libsandbox.Spec{
		WorkspaceRoot: t.TempDir(),
		Home:          t.TempDir(),
	}, "true")

	require.Nil(t, cmd, "no command may be returned when the wall cannot be built")
	require.ErrorIs(t, err, libsandbox.ErrIsolation)
}
