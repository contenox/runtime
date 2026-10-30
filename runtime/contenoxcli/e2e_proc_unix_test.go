//go:build unix

package contenoxcli

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const e2eExeSuffix = ""

// e2eOwnProcessGroup puts the child in its own process group so the whole tree
// can be signalled.
func e2eOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// e2eTerminateTree asks the process group to shut down gracefully (SIGTERM).
// It reports whether a graceful request was sent.
func e2eTerminateTree(pid int) bool {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	return true
}

// e2eKillTree force-kills the process group.
func e2eKillTree(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// e2eTerminateProcess asks a single process to exit gracefully.
func e2eTerminateProcess(p *os.Process) {
	_ = p.Signal(syscall.SIGTERM)
}

func childPIDs(parentPid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue
		}
		if ppid, ok := procPPID(pid); ok && ppid == parentPid {
			out = append(out, pid)
		}
	}
	return out
}

func procPPID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+1 >= len(s) {
		return 0, false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

func pidAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
