//go:build !windows

package modeldinstall

import (
	"os/exec"
	"syscall"
)

// Detach starts the shared daemon in a session independent of the client.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
