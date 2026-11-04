//go:build windows

package agenthost_test

import (
	"testing"

	"github.com/contenox/runtime/libsandbox"
)

func requireSandboxable(t *testing.T) {
	t.Helper()
	if err := libsandbox.Preflight(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
}
