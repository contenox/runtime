package acpexec_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp/acpexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures a Unix shell used to provide (cat, sh -c, sleep, a child
// process tree) are served by re-running this test binary in fixture mode.
// That keeps every byte-exact assertion valid on Windows and Linux alike.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACPEXEC_TEST_FIXTURE"); mode != "" {
		runFixture(mode)
		return
	}
	os.Exit(m.Run())
}

func runFixture(mode string) {
	switch mode {
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "sleep":
		time.Sleep(300 * time.Second)
	case "exit3":
		os.Exit(3)
	case "stderr":
		_, _ = fmt.Fprintln(os.Stderr, "oops")
	case "child-exit":
		_, _ = fmt.Fprintln(os.Stdout, "from-child")
	case "tree":
		child := fixture("tree-child")
		_ = child.Start()
		time.Sleep(300 * time.Second)
	case "tree-child":
		time.Sleep(300 * time.Second)
	}
	os.Exit(0)
}

// fixture builds a command that runs this test binary in the given fixture
// mode. It behaves identically on Unix and Windows.
func fixture(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ACPEXEC_TEST_FIXTURE="+mode)
	return cmd
}

func TestSpawn_EchoesStdinToStdout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := acpexec.Spawn(ctx, fixture("echo"))
	require.NoError(t, err)

	_, err = proc.Write([]byte("hello\n"))
	require.NoError(t, err)

	r := bufio.NewReader(proc)
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "hello\n", line)

	require.NoError(t, proc.Close())
}

func TestSpawn_ProcessExitingOnItsOwnYieldsEOFAndCloseDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := acpexec.Spawn(ctx, fixture("child-exit"))
	require.NoError(t, err)

	out, err := io.ReadAll(proc)
	require.NoError(t, err)
	assert.Equal(t, "from-child\n", string(out))

	closeDone := make(chan error, 1)
	go func() { closeDone <- proc.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung after the subprocess had already exited on its own")
	}
}

func TestSpawn_DoubleCloseIsSafeAndReturnsSameResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := acpexec.Spawn(ctx, fixture("echo"))
	require.NoError(t, err)

	err1 := proc.Close()
	err2 := proc.Close()
	assert.NoError(t, err1)
	assert.Equal(t, err1, err2)
}

func TestSpawn_CloseKillsAProcessThatIgnoresStdinClosing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	proc, err := acpexec.Spawn(ctx, fixture("sleep"), acpexec.WithKillGrace(200*time.Millisecond))
	require.NoError(t, err)

	closeDone := make(chan error, 1)
	start := time.Now()
	go func() { closeDone <- proc.Close() }()
	select {
	case err := <-closeDone:
		assert.Less(t, time.Since(start), 10*time.Second, "Close should have killed the process well within its 30s sleep")
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not kill a subprocess ignoring stdin closing")
	}
}

func TestSpawn_CloseKillsTheWholeProcessTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var stderr acpexec.LockedBuffer
	proc, err := acpexec.Spawn(ctx, fixture("tree"),
		acpexec.WithStderr(&stderr),
		acpexec.WithKillGrace(200*time.Millisecond))
	require.NoError(t, err)

	closeDone := make(chan error, 1)
	go func() { closeDone <- proc.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err, "kill-path teardown of the whole tree must be clean")
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung: the backgrounded grandchild kept the pipes open, so the process group kill did not work")
	}
}

func TestSpawn_CloseSurfacesASelfInflictedBadExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := acpexec.Spawn(ctx, fixture("exit3"))
	require.NoError(t, err)

	_, err = io.ReadAll(proc)
	require.NoError(t, err)

	err = proc.Close()
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.ExitCode())
}

func TestSpawn_CtxCancellationTearsDownTheProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	proc, err := acpexec.Spawn(ctx, fixture("echo"), acpexec.WithKillGrace(200*time.Millisecond))
	require.NoError(t, err)

	cancel()

	readDone := make(chan error, 1)
	go func() {
		_, err := proc.Read(make([]byte, 16))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		require.Error(t, err, "Read must not succeed once ctx is cancelled and the process is torn down")
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not unblock after ctx cancellation")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- proc.Close() }()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung after ctx cancellation had already torn the process down")
	}
}

func TestSpawn_StderrIsForwardedToConfiguredWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stderr acpexec.LockedBuffer
	proc, err := acpexec.Spawn(ctx, fixture("stderr"), acpexec.WithStderr(&stderr))
	require.NoError(t, err)

	_, _ = io.ReadAll(proc)
	require.NoError(t, proc.Close())

	assert.Equal(t, "oops\n", stderr.String())
}

func TestSpawn_PipeSetupFailureLeavesNothingToCleanUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := fixture("echo")
	r, w := io.Pipe()
	defer func() { _ = r.Close(); _ = w.Close() }()
	cmd.Stdin = r

	_, err := acpexec.Spawn(ctx, cmd)
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.DeadlineExceeded))
}
