//go:build windows

package agenthost

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/contenox/runtime/libsandbox"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func assertForeignConfined(t *testing.T, cmd *exec.Cmd, foreign string) {
	t.Helper()
	require.NotNil(t, cmd.SysProcAttr, "a foreign agent runs under the sandbox's token")
	require.NotZero(t, cmd.SysProcAttr.Token, "a foreign agent runs under the sandbox's low-integrity token")
	require.NotZero(t, cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED, "a foreign agent starts suspended until it is in its job")
	require.NoError(t, libsandbox.Started(cmd, errors.New("not started in this test")))
}
