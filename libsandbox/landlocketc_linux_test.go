//go:build linux

package libsandbox_test

import (
	"context"
	"os"
	"testing"

	"github.com/contenox/runtime/libsandbox"
	"github.com/stretchr/testify/require"
)

func TestIntegration_EtcNarrowed_DeniesUngrantedEtcFile(t *testing.T) {
	if !landlockSupported() {
		t.Skip("landlock filesystem ABI unavailable on this kernel")
	}
	if !usernsNetnsSupported() {
		t.Skip("unprivileged user+network namespaces unavailable on this host")
	}

	target := findUngrantedWorldReadableEtcFile(t)

	ws := t.TempDir()
	home := t.TempDir()
	spec := libsandbox.Spec{
		WorkspaceRoot: ws,
		Home:          home,
		EnvSet:        map[string]string{probeEnv: "read", probePathEnv: target},
	}

	cmd, err := libsandbox.Command(context.Background(), spec, "/proc/self/exe")
	require.NoError(t, err)
	require.Equal(t, exitDenied, exitCode(cmd.Run()),
		"an ungranted /etc file (%s) must be unreadable under the narrowed /etc grant", target)
}

func findUngrantedWorldReadableEtcFile(t *testing.T) string {
	t.Helper()
	granted := map[string]bool{
		"ld.so.cache": true, "ld.so.conf": true, "ld.so.preload": true,
		"nsswitch.conf": true, "resolv.conf": true, "hosts": true,
		"host.conf": true, "gai.conf": true, "services": true, "protocols": true,
		"passwd": true, "group": true, "localtime": true,
	}
	entries, err := os.ReadDir("/etc")
	if err != nil {
		t.Skip("cannot read /etc to find a probe target")
	}
	for _, e := range entries {
		if e.IsDir() || granted[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Mode().Perm()&0o004 == 0 {
			continue
		}
		return "/etc/" + e.Name()
	}
	t.Skip("no ungranted world-readable regular /etc file present to probe")
	return ""
}
