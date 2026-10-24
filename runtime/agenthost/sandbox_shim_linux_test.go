//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	smokeProbeEnv = "CONTENOX_AGENTHOST_SANDBOX_PROBE"

	exitProbeUnreachable = 20
	exitProbeConnected   = 21
	exitProbeOther       = 22
	exitShimFailed       = 5
)

func TestMain(m *testing.M) {
	if handled, err := libsandbox.ShimMain(); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "agenthost sandbox shim:", err)
			os.Exit(exitShimFailed)
		}
		os.Exit(0)
	}
	if os.Getenv(smokeProbeEnv) != "" {
		os.Exit(runSmokeProbe())
	}
	os.Exit(m.Run())
}

func runSmokeProbe() int {
	c, err := net.DialTimeout("tcp", "192.0.2.1:80", 3*time.Second)
	if err == nil {
		_ = c.Close()
		return exitProbeConnected
	}
	if errors.Is(err, unix.ENETUNREACH) || errors.Is(err, unix.EHOSTUNREACH) {
		return exitProbeUnreachable
	}
	return exitProbeOther
}

func TestSmoke_SandboxConfinesSpawnedAgent(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox unavailable: needs landlock + unprivileged user/network namespaces")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONTENOX_SANDBOX_NETWORK_WALL", "1")

	a := &ExternalACPAgent{Config: runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   copyOfTestBinary(t),
		Cwd:       t.TempDir(),
		Env:       map[string]string{smokeProbeEnv: "net-connect"},
	}}

	cmd, err := buildAgentCmd(context.Background(), a)
	require.NoError(t, err)

	err = cmd.Run()
	require.Equal(t, exitProbeUnreachable, exitCode(err),
		"the spawned agent must be confined: an outbound connection is network-unreachable inside the empty netns")
}

func copyOfTestBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	src, err := os.ReadFile(self)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "fake-agent")
	require.NoError(t, os.WriteFile(dst, src, 0o755))
	return dst
}

func sandboxSupported() bool {
	return landlockSupported() && usernsNetnsSupported()
}

func landlockSupported() bool {
	r, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION))
	return e == 0 && int(r) >= 1
}

func usernsNetnsSupported() bool {
	cmd := exec.Command("/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWNET,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	return cmd.Run() == nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
