//go:build windows

package libsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modNtdll            = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProcess = modNtdll.NewProc("NtResumeProcess")
)

func started(cmd *exec.Cmd, startErr error) error {
	v, ok := pendingStarts.LoadAndDelete(cmd)
	if !ok {
		return nil
	}
	ps := v.(*pendingStart)
	defer ps.token.Close()
	if startErr != nil || cmd.Process == nil {
		removeAgentTemp(ps.tmp)
		return nil
	}

	access := uint32(windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_SUSPEND_RESUME | windows.SYNCHRONIZE)
	proc, err := windows.OpenProcess(access, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("%w: open the agent process to confine it: %v", ErrIsolation, err)
	}
	fail := func(step string, err error) error {
		_ = windows.TerminateProcess(proc, 1)
		_ = windows.CloseHandle(proc)
		removeAgentTemp(ps.tmp)
		return fmt.Errorf("%w: %s: %v", ErrIsolation, step, err)
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fail("create the agent's job object", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return fail("limit the agent's job object", err)
	}
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return fail("assign the agent to its job object", err)
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(proc)); r != 0 {
		_ = windows.CloseHandle(job)
		return fail("resume the agent", fmt.Errorf("NTSTATUS 0x%x", uint32(r)))
	}

	go func() {
		_, _ = windows.WaitForSingleObject(proc, windows.INFINITE)
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(proc)
		removeAgentTemp(ps.tmp)
	}()
	return nil
}

func removeAgentTemp(dir string) {
	if dir == "" {
		return
	}
	for i := 0; i < 10; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
