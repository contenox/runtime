//go:build windows

package libsandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/contenox/runtime/libtracker"
	"golang.org/x/sys/windows"
)

const (
	lowIntegritySID     = "S-1-16-4096"
	administratorsSID   = "S-1-5-32-544"
	disableMaxPrivilege = 0x1
)

var (
	modAdvapi32               = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = modAdvapi32.NewProc("CreateRestrictedToken")
)

var lowWriteLabel = regexp.MustCompile(`\(ML;[A-Z]*;NW;;;LW\)`)

type pendingStart struct {
	token windows.Token
	tmp   string
}

var pendingStarts sync.Map

func applyIsolation(ctx context.Context, cmd *exec.Cmd, spec Spec, tracker libtracker.ActivityTracker) error {
	if spec.NetworkWall {
		return fmt.Errorf("%w: the network wall is implemented only on Linux; on Windows the agent keeps the host network, so drop the Net carve-outs and CONTENOX_SANDBOX_NETWORK_WALL", ErrIsolation)
	}
	if spec.SyscallTap {
		return PreflightSyscallTap()
	}
	plan, err := buildPlan(spec, cmd.Path, cmd.Args)
	if err != nil {
		return err
	}

	for _, root := range writableRoots(plan) {
		changed, err := ensureLowWriteLabel(root, plan.Home)
		if err != nil {
			return err
		}
		if changed {
			_, reportChange, end := tracker.Start(ctx, "label", "sandbox_writable_root", "path", root)
			reportChange(root, map[string]any{"integrity": "low"})
			end()
		}
	}

	base, err := agentTempDir()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(base, "agent-")
	if err != nil {
		return fmt.Errorf("%w: create a temp folder for this agent in %q: %v", ErrIsolation, base, err)
	}
	cmd.Env = OverlayEnv(cmd.Env, map[string]string{"TEMP": tmp, "TMP": tmp})

	tok, err := lowIntegrityToken()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("%w: build the agent's low-integrity token: %v", ErrIsolation, err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	pendingStarts.Store(cmd, &pendingStart{token: tok, tmp: tmp})
	return nil
}

func writableRoots(plan isolationPlan) []string {
	roots := []string{plan.Workspace}
	for _, c := range plan.FS {
		if c.Mode == ModeRW {
			roots = append(roots, c.Path)
		}
	}
	return roots
}

func ensureLowWriteLabel(path, home string) (bool, error) {
	if err := refuseLabel(path, home); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return false, fmt.Errorf("%w: create writable carve-out %q: %v", ErrIsolation, path, err)
		}
		info, err = os.Stat(path)
	}
	if err != nil {
		return false, fmt.Errorf("%w: writable root %q: %v", ErrIsolation, path, err)
	}
	current, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("%w: read the integrity label of %q: %v", ErrIsolation, path, err)
	}
	if lowWriteLabel.MatchString(current.String()) {
		return false, nil
	}
	sddl := "S:(ML;;NW;;;LW)"
	if info.IsDir() {
		sddl = "S:(ML;OICI;NW;;;LW)"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return false, fmt.Errorf("%w: build low-integrity label: %v", ErrIsolation, err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return false, fmt.Errorf("%w: build low-integrity label: %v", ErrIsolation, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return false, fmt.Errorf("%w: lower the integrity label of %q so the agent can write there: %v", ErrIsolation, path, err)
	}
	return true, nil
}

func refuseLabel(path, home string) error {
	clean := filepath.Clean(path)
	if vol := filepath.VolumeName(clean); vol != "" && strings.EqualFold(strings.TrimSuffix(clean, `\`), vol) {
		return fmt.Errorf("%w: %q is a drive root; the agent's writable roots must be folders inside it", ErrInvalidSpec, path)
	}
	if home != "" && strings.EqualFold(clean, filepath.Clean(home)) {
		return fmt.Errorf("%w: %q is the operator's home; grant the folders inside it the agent needs instead", ErrInvalidSpec, path)
	}
	for _, dir := range platformExecDirs() {
		if pathWithin(clean, []string{dir}) {
			return fmt.Errorf("%w: %q is a system folder; the agent may not write there", ErrInvalidSpec, path)
		}
	}
	return nil
}

func agentTempDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "Contenox", "agent-temp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("%w: create the agent's temp folder %q: %v", ErrIsolation, dir, err)
	}
	if _, err := ensureLowWriteLabel(dir, ""); err != nil {
		return "", err
	}
	return dir, nil
}

func lowIntegrityToken() (windows.Token, error) {
	var self windows.Token
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &self); err != nil {
		return 0, fmt.Errorf("open process token: %w", err)
	}
	defer self.Close()

	admins, err := windows.StringToSid(administratorsSID)
	if err != nil {
		return 0, err
	}
	disable := []windows.SIDAndAttributes{{Sid: admins}}
	var out windows.Token
	r, _, callErr := procCreateRestrictedToken.Call(
		uintptr(self),
		disableMaxPrivilege,
		uintptr(len(disable)),
		uintptr(unsafe.Pointer(&disable[0])),
		0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", callErr)
	}

	low, err := windows.StringToSid(lowIntegritySID)
	if err != nil {
		_ = out.Close()
		return 0, err
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: low, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(out, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		_ = out.Close()
		return 0, fmt.Errorf("set low integrity: %w", err)
	}
	return out, nil
}
