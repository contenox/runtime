package agentdecl

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnit_CompileLock_ARestartRightAfterACrashOutlivesTheStaleLock(t *testing.T) {
	prevStale, prevWait := compileLockStale, compileLockWait
	compileLockStale, compileLockWait = 200*time.Millisecond, 400*time.Millisecond
	defer func() { compileLockStale, compileLockWait = prevStale, prevWait }()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, GeneratedDirName), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, GeneratedDirName, ".compile.lock"), nil, 0o600))

	release, err := CompileLock(home)
	require.NoError(t, err, "a lock left by a killed process must not block the next start")
	release()
}

func TestUnit_CompileLock_WaitsLongerThanALockTakesToGoStale(t *testing.T) {
	require.Greater(t, compileLockWait, compileLockStale)
}
