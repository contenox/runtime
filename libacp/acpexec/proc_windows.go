//go:build windows

package acpexec

import (
	"errors"
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

func setProcessGroup(cmd *exec.Cmd) {}

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	taskkill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	hideWindow(taskkill)
	_ = taskkill.Run()
	_ = cmd.Process.Kill()
}

func exitFromKill(waitErr error) bool {
	var exitErr *exec.ExitError
	return errors.As(waitErr, &exitErr)
}
