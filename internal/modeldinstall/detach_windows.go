package modeldinstall

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Detach starts the shared daemon outside the client's console process group.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
