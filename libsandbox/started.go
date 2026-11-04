package libsandbox

import "os/exec"

func Started(cmd *exec.Cmd, startErr error) error {
	return started(cmd, startErr)
}
