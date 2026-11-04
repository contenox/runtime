//go:build !linux && !windows

package agenthost_test

import "testing"

func requireSandboxable(t *testing.T) {
	t.Helper()
	t.Skip("external ACP agent hosting needs the libsandbox wall, which exists on Linux and Windows")
}
