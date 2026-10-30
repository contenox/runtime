//go:build !linux

package libsandbox

import "fmt"

func Preflight() error {
	return fmt.Errorf("%w: OS-level agent confinement is implemented only on Linux; "+
		"the agent sandbox cannot be built on this host", ErrIsolation)
}

// PreflightSyscallTap reports that the syscall tap is Linux-only.
func PreflightSyscallTap() error {
	return fmt.Errorf("%w: the syscall tap (seccomp user-notify) is implemented only on Linux", ErrIsolation)
}
