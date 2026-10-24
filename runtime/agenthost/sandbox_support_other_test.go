//go:build !linux

package agenthost_test

import "testing"

func requireSandboxable(t *testing.T) {
	t.Helper()
	t.Skip("external ACP agent hosting is Linux-only (the libsandbox wall requires Linux)")
}
