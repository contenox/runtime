//go:build windows

package contenoxcli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const e2eExeSuffix = ".exe"

// e2eOwnProcessGroup gives the child its own process group so console control
// events sent to the test do not reach it.
func e2eOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// e2eTerminateTree: Windows has no SIGTERM for console programs, so there is no
// graceful request to send. It reports false so callers go straight to the kill.
func e2eTerminateTree(pid int) bool { return false }

// e2eKillTree force-kills the process and its descendants.
func e2eKillTree(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// e2eTerminateProcess: no graceful signal exists on Windows; the callers close
// stdin first and fall back to Kill.
func e2eTerminateProcess(p *os.Process) {}

// childPIDs lists the direct children of parentPid.
func childPIDs(parentPid int) []int {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_Process -Filter 'ParentProcessId="+strconv.Itoa(parentPid)+"').ProcessId").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, convErr := strconv.Atoi(f); convErr == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func pidAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}
