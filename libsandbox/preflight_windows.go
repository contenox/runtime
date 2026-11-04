//go:build windows

package libsandbox

import "fmt"

func Preflight() error {
	tok, err := lowIntegrityToken()
	if err != nil {
		return fmt.Errorf("%w: cannot build a low-integrity token for the agent on this host: %v", ErrIsolation, err)
	}
	_ = tok.Close()
	return nil
}

func PreflightSyscallTap() error {
	return fmt.Errorf("%w: the syscall tap (seccomp user-notify) is implemented only on Linux", ErrIsolation)
}
