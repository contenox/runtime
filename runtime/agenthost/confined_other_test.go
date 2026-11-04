//go:build !windows

package agenthost

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func assertForeignConfined(t *testing.T, cmd *exec.Cmd, foreign string) {
	t.Helper()
	require.NotEqual(t, foreign, cmd.Path,
		"a foreign agent is reached through the sandbox shim, never exec'd directly")
}
