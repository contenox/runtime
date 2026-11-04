//go:build windows

package libsandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func windowsTestSpec(t *testing.T, workspace string) Spec {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	return Spec{
		WorkspaceRoot: workspace,
		Home:          home,
		EnvAllow:      DefaultEnvPolicy().Resolve(os.Environ()),
	}
}

func cmdExe() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
}

func TestUnit_WindowsCommand_WritesOnlyInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))

	script := `echo inside>` + filepath.Join(workspace, "in.txt") + ` & echo outside>` + filepath.Join(outside, "out.txt") + ` & mkdir %TEMP%\probe-dir & echo tmp>%TEMP%\probe-dir\t.txt & type %TEMP%\probe-dir\t.txt & rmdir /s /q %TEMP%\probe-dir & echo %TEMP%`
	cmd, err := Command(context.Background(), windowsTestSpec(t, workspace), cmdExe(), "/d", "/c", script)
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	startErr := cmd.Start()
	require.NoError(t, Started(cmd, startErr))
	require.NoError(t, startErr)
	_ = cmd.Wait()

	require.FileExists(t, filepath.Join(workspace, "in.txt"))
	require.NoFileExists(t, filepath.Join(outside, "out.txt"))
	require.Contains(t, strings.ToLower(stdout.String()), strings.ToLower(filepath.Join(os.TempDir(), "Contenox", "agent-temp")))
	require.Contains(t, stdout.String(), "tmp", "the agent can create folders and files in its temp folder: %s", stderr.String())
}

func TestUnit_WindowsCommand_RunsInJob(t *testing.T) {
	workspace := t.TempDir()
	cmd, err := Command(context.Background(), windowsTestSpec(t, workspace), cmdExe(), "/d", "/c", "ping -n 3 127.0.0.1 >nul")
	require.NoError(t, err)
	startErr := cmd.Start()
	require.NoError(t, Started(cmd, startErr))
	require.NoError(t, startErr)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	require.NoError(t, err)
	defer windows.CloseHandle(h)
	var inJob int32
	r, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob").Call(uintptr(h), 0, uintptr(unsafe.Pointer(&inJob)))
	require.NotZero(t, r, callErr)
	require.NotZero(t, inJob)

	var tok windows.Token
	hq, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(cmd.Process.Pid))
	require.NoError(t, err)
	defer windows.CloseHandle(hq)
	require.NoError(t, windows.OpenProcessToken(hq, windows.TOKEN_QUERY, &tok))
	defer tok.Close()
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, nil, 0, &n)
	buf := make([]byte, n)
	require.NoError(t, windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, &buf[0], n, &n))
	label := (*windows.Tokenmandatorylabel)(unsafePointer(&buf[0]))
	require.Equal(t, lowIntegritySID, label.Label.Sid.String())
}

func TestUnit_WindowsCommand_RefusesBroadWritableRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, root := range []string{home, os.Getenv("SystemDrive") + `\`, filepath.Join(os.Getenv("SystemRoot"), "System32")} {
		spec := windowsTestSpec(t, t.TempDir())
		spec.FS = []FSCarveout{{Path: root, Mode: ModeRW, Needs: "test"}}
		_, err := Command(context.Background(), spec, cmdExe(), "/c", "exit")
		require.ErrorIs(t, err, ErrInvalidSpec, root)
	}
}

func TestUnit_WindowsCommand_RefusesNetworkWall(t *testing.T) {
	spec := windowsTestSpec(t, t.TempDir())
	spec.NetworkWall = true
	_, err := Command(context.Background(), spec, cmdExe(), "/c", "exit")
	require.ErrorIs(t, err, ErrIsolation)
}

func TestUnit_WindowsScrubEnv_FoldsNames(t *testing.T) {
	env := scrubEnv([]string{`Path=C:\Windows`, `SystemRoot=C:\Windows`, `GITHUB_TOKEN=x`}, []string{"PATH", "SYSTEMROOT"}, map[string]string{"path": `C:\Windows\System32`}, `C:\Users\op`)
	joined := strings.Join(env, "\n")
	require.Contains(t, joined, `SystemRoot=C:\Windows`)
	require.Equal(t, `C:\Windows\System32`, lookupEnv(env, "PATH"))
	require.NotContains(t, joined, "GITHUB_TOKEN")
	require.Equal(t, 1, strings.Count(strings.ToUpper(joined), "PATH="))
}

func unsafePointer(b *byte) unsafe.Pointer { return unsafe.Pointer(b) }

func TestUnit_WindowsCommand_EachAgentGetsItsOwnTemp(t *testing.T) {
	workspace := t.TempDir()
	run := func() string {
		cmd, err := Command(context.Background(), windowsTestSpec(t, workspace), cmdExe(), "/d", "/c", "echo %TEMP%")
		require.NoError(t, err)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		startErr := cmd.Start()
		require.NoError(t, Started(cmd, startErr))
		require.NoError(t, startErr)
		require.NoError(t, cmd.Wait())
		return strings.TrimSpace(stdout.String())
	}
	first, second := run(), run()
	require.NotEqual(t, first, second, "agents running at the same time must not share a temp folder")
	require.Eventually(t, func() bool {
		_, err := os.Stat(first)
		return os.IsNotExist(err)
	}, 5*time.Second, 50*time.Millisecond, "an agent's temp folder goes away when the agent ends")
}
