//go:build !windows

package libsandbox

import "os/exec"

func started(cmd *exec.Cmd, startErr error) error {
	return nil
}
