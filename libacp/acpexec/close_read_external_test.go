package acpexec_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp/acpexec"
	"github.com/stretchr/testify/require"
)

func TestProcessOwnedShutdownLeavesEOFAndPreservesChildExitFailure(t *testing.T) {
	for _, mode := range []string{"clean", "failed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			process, cancel := spawnOwnedShutdownProcess(t, mode)
			if mode == "cancelled" {
				cancel()
			}
			closeErr := process.Close()
			if mode == "failed" {
				var exitErr *exec.ExitError
				require.ErrorAs(t, closeErr, &exitErr)
				require.Equal(t, 7, exitErr.ExitCode())
			} else {
				require.NoError(t, closeErr)
			}
			n, readErr := process.Read(make([]byte, 1))
			require.Zero(t, n)
			require.ErrorIs(t, readErr, io.EOF, "the owner has finished shutdown; no live read failed")
			require.Equal(t, closeErr, process.Close(), "repeated Close preserves the process result")
		})
	}
}

func TestProcessKeepsFinalOutputAfterChildHasExited(t *testing.T) {
	process, _ := spawnOwnedShutdownProcess(t, "final-output")
	var writeErr error
	require.Eventually(t, func() bool {
		_, writeErr = process.Write([]byte{0})
		return errors.Is(writeErr, os.ErrClosed)
	}, 5*time.Second, time.Millisecond, "Wait must have closed its stdin pipe")

	output, err := io.ReadAll(process)
	require.NoError(t, err)
	require.Equal(t, "final notification\n", string(output))
	require.NoError(t, process.Close())
}

func TestProcessRefusesClaimedStdioWithoutStartingChild(t *testing.T) {
	for _, stream := range []string{"stdin", "stdout"} {
		t.Run(stream, func(t *testing.T) {
			command := ownedShutdownCommand(t, "clean")
			if stream == "stdin" {
				command.Stdin = strings.NewReader("")
			} else {
				command.Stdout = io.Discard
			}
			process, err := acpexec.Spawn(t.Context(), command)
			require.ErrorContains(t, err, stream+" pipe")
			require.Nil(t, process)
			require.Nil(t, command.Process)
		})
	}
}

func TestProcessStartFailureClosesClaimedStdio(t *testing.T) {
	command := ownedShutdownCommand(t, "clean")
	command.Path = filepath.Join(command.Dir, "missing-executable")
	process, err := acpexec.Spawn(t.Context(), command)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Nil(t, process)
	_, stdinErr := command.Stdin.Read(make([]byte, 1))
	require.ErrorIs(t, stdinErr, os.ErrClosed)
	_, stdoutErr := command.Stdout.Write([]byte("final notification\n"))
	require.ErrorIs(t, stdoutErr, os.ErrClosed)
}

func spawnOwnedShutdownProcess(t *testing.T, mode string) (*acpexec.Process, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	process, err := acpexec.Spawn(ctx, ownedShutdownCommand(t, mode))
	require.NoError(t, err)
	t.Cleanup(func() { _ = process.Close() })
	return process, cancel
}

func ownedShutdownCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	root := t.TempDir()
	scratch := filepath.Join(root, "tmp")
	require.NoError(t, os.Mkdir(scratch, 0o700))
	command := exec.Command(self, "-test.run=^TestProcessOwnedShutdownHelper$")
	command.Dir = root
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + root, "USERPROFILE=" + root,
		"TMPDIR=" + scratch, "GOTMPDIR=" + scratch,
		"CONTENOX_ACPEXEC_SHUTDOWN_HELPER=" + mode,
	}
	return command
}

func TestProcessOwnedShutdownHelper(t *testing.T) {
	mode := os.Getenv("CONTENOX_ACPEXEC_SHUTDOWN_HELPER")
	if mode == "" {
		return
	}
	if mode == "final-output" {
		if _, err := io.WriteString(os.Stdout, "final notification\n"); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(8)
	}
	if mode == "failed" {
		os.Exit(7)
	}
	os.Exit(0)
}
