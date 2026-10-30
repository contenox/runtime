//go:build windows

package winproc_test

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/contenox/runtime/runtime/internal/winproc"
)

func TestUnit_Hide_MarksTheCommandWindowless(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	winproc.Hide(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("SysProcAttr = %+v, want HideWindow", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestUnit_Hide_KeepsExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	winproc.Hide(cmd)
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("CreationFlags = %#x, lost CREATE_NEW_PROCESS_GROUP", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
