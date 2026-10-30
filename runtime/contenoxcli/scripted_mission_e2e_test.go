//go:build unix

package contenoxcli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

const (
	scriptedLandsChain = `{
  "id": "agent-scripted-lands",
  "description": "Scripted e2e: finish the mission landed via mission_finish.",
  "tasks": [
    {
      "id": "finish",
      "handler": "tools",
      "tools": {"name": "mission", "tool_name": "mission_finish", "args": {"status": "landed", "reason": "scripted landing"}},
      "transition": {"branches": [{"operator": "default", "goto": "done"}]}
    },
    {"id": "done", "handler": "noop", "transition": {"branches": [{"operator": "default", "goto": "end"}]}}
  ]
}`

	scriptedBlockerChain = `{
  "id": "agent-scripted-blocker",
  "description": "Scripted e2e: file a blocker report (needs attention).",
  "tasks": [
    {
      "id": "block",
      "handler": "tools",
      "tools": {"name": "mission", "tool_name": "mission_report", "args": {"kind": "blocker", "summary": "scripted blocker needs attention"}},
      "transition": {"branches": [{"operator": "default", "goto": "done"}]}
    },
    {"id": "done", "handler": "noop", "transition": {"branches": [{"operator": "default", "goto": "end"}]}}
  ]
}`

	scriptedProgressChain = `{
  "id": "agent-scripted-progress",
  "description": "Scripted e2e: file a single progress report, then idle (intermediate only).",
  "tasks": [
    {
      "id": "progress",
      "handler": "tools",
      "tools": {"name": "mission", "tool_name": "mission_report", "args": {"kind": "progress", "summary": "scripted progress, still working"}},
      "transition": {"branches": [{"operator": "default", "goto": "done"}]}
    },
    {"id": "done", "handler": "noop", "transition": {"branches": [{"operator": "default", "goto": "end"}]}}
  ]
}`
)

const scriptedPolicy = "hitl-policy-default.json"

var missionIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

func TestSystem_ScriptedMissionSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scripted-mission system e2e: builds contenox and boots a real serve")
	}

	bin := buildScriptedContenoxBin(t)

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	workspaceDir := filepath.Join(root, "workspace")
	dataDir := filepath.Join(workspaceDir, ".contenox")
	dbPath := filepath.Join(homeDir, ".contenox-harness", "local.db")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	baseEnv := append(os.Environ(),
		"HOME="+homeDir,
		"CONTENOX_DEFAULT_MODEL=scripted-e2e-fake-model",
		"CONTENOX_DEFAULT_PROVIDER=ollama",
	)

	runScriptedCLI(t, bin, baseEnv, "--data-dir", dataDir, "--db", dbPath, "init", "--force")
	chainsDir := filepath.Join(root, "chains")
	require.NoError(t, os.MkdirAll(chainsDir, 0o755))
	landsPath := filepath.Join(chainsDir, "lands.json")
	blockerPath := filepath.Join(chainsDir, "blocker.json")
	progressPath := filepath.Join(chainsDir, "progress.json")
	require.NoError(t, os.WriteFile(landsPath, []byte(scriptedLandsChain), 0o644))
	require.NoError(t, os.WriteFile(blockerPath, []byte(scriptedBlockerChain), 0o644))
	require.NoError(t, os.WriteFile(progressPath, []byte(scriptedProgressChain), 0o644))

	seedScriptedAgents(t, dbPath, bin, map[string]string{
		"agent-scripted-lands":    landsPath,
		"agent-scripted-blocker":  blockerPath,
		"agent-scripted-progress": progressPath,
	})

	port := freePort(t)
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	serveEnv := append(append([]string{}, baseEnv...),
		"ADDR=127.0.0.1",
		fmt.Sprintf("PORT=%d", port),
		"TOKEN=",
	)
	serveLog := startScriptedServe(t, bin, serveEnv, dataDir, dbPath, serverURL)

	fireArgs := func(agent string, extra ...string) []string {
		base := []string{
			"mission", "fire", "-q", "--wait",
			"--server", serverURL,
			"--agent", agent,
			"--policy", scriptedPolicy,
			"--intent", "scripted mission for " + agent,
			"--wait-interval", "300ms",
			"--wait-timeout", "90s",
		}
		return append(base, extra...)
	}

	t.Run("landed_exits_zero_and_id_is_machine_readable", func(t *testing.T) {
		stdout, stderr, code := runScripted(t, bin, baseEnv, fireArgs("agent-scripted-lands")...)
		require.Equalf(t, 0, code, "a landed mission must exit 0.\nstdout:%s\nstderr:%s\nserve log:\n%s", stdout, stderr, serveLog())

		mid := strings.TrimSpace(stdout)
		require.Regexp(t, missionIDPattern, mid, "with -q, stdout is exactly the mission id (a bare UUID)")
		require.NotContains(t, stdout, "Mission fired", "quiet mode prints only the id to stdout; prose goes to stderr")

		showOut, _, showCode := runScripted(t, bin, baseEnv,
			"mission", "show", "--server", serverURL, "--json", mid)
		require.Equal(t, 0, showCode, "the captured id resolves via `mission show`")
		require.Contains(t, showOut, mid)
		require.Contains(t, showOut, `"status": "landed"`, "the mission the id names really landed")
	})

	t.Run("success_chains_with_and", func(t *testing.T) {
		script := shJoin(bin, fireArgs("agent-scripted-lands")) + " && " + shJoin(bin, fireArgs("agent-scripted-lands"))
		stdout, stderr, code := runScripted(t, "/bin/sh", baseEnv, "-c", script)
		require.Equalf(t, 0, code, "fire --wait && fire --wait must compose on success.\nstdout:%s\nstderr:%s", stdout, stderr)

		ids := missionIDsIn(stdout)
		require.Len(t, ids, 2, "both fires ran and each printed its mission id (the second because the first exited 0)")
		require.NotEqual(t, ids[0], ids[1], "each fire is a distinct mission")
	})

	t.Run("blocker_breaks_the_chain", func(t *testing.T) {
		script := shJoin(bin, fireArgs("agent-scripted-blocker")) + " && " + shJoin(bin, fireArgs("agent-scripted-lands"))
		stdout, stderr, code := runScripted(t, "/bin/sh", baseEnv, "-c", script)
		require.Equalf(t, missionWaitBlocked, code, "a blocker exits 2 and breaks the && chain.\nstdout:%s\nstderr:%s", stdout, stderr)

		ids := missionIDsIn(stdout)
		require.Len(t, ids, 1, "only the blocker fire ran; the second fire never started because the first exited non-zero")
		require.Contains(t, stderr, "BLOCKED", "the wait's stderr names why it stopped")
	})

	t.Run("progress_only_times_out", func(t *testing.T) {
		stdout, stderr, code := runScripted(t, bin, baseEnv,
			fireArgs("agent-scripted-progress", "--wait-timeout", "4s", "--wait-interval", "300ms")...)
		require.Equalf(t, missionWaitTimeout, code, "a unit filing only intermediate reports rides to the deadline: exit 3.\nstdout:%s\nstderr:%s", stdout, stderr)
		require.Regexp(t, missionIDPattern, strings.TrimSpace(stdout), "the id is still printed to stdout before the wait blocks")
		require.Contains(t, stderr, "timed out")
	})
}

func buildScriptedContenoxBin(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "contenox")
	out, err := exec.Command("go", "build", "-o", binPath, "github.com/contenox/runtime/cmd/contenox").CombinedOutput()
	require.NoErrorf(t, err, "build contenox:\n%s", out)
	return binPath
}

func seedScriptedAgents(t *testing.T, dbPath, bin string, agentsToChain map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, dbPath, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	registry := agentregistryservice.New(db)
	for name, chainPath := range agentsToChain {
		agent := &runtimetypes.Agent{Name: name, Enabled: true}
		require.NoError(t, agent.SetExternalACPConfig(runtimetypes.ExternalACPConfig{
			Transport: runtimetypes.ExternalACPTransportStdio,
			Command:   bin,
			Args:      []string{"acp", "--auto"},
			Env:       map[string]string{"CONTENOX_ACP_CHAIN_PATH": chainPath},
		}))
		require.NoError(t, registry.Create(ctx, agent), "seed external agent %q", name)
	}
}

func runScriptedCLI(t *testing.T, bin string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "contenox %v:\n%s", args, out)
}

func runScripted(t *testing.T, name string, env []string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("run %s %v: %v\nstderr:%s", name, args, err, stderr.String())
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func shJoin(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shQuote(bin))
	for _, a := range args {
		parts = append(parts, shQuote(a))
	}
	return strings.Join(parts, " ")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func missionIDsIn(stdout string) []string {
	var ids []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if missionIDPattern.MatchString(line) {
			ids = append(ids, line)
		}
	}
	return ids
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

type serveBootOutcome struct {
	log       func() string
	ready     bool
	diedEarly bool
}

func startScriptedServe(t *testing.T, bin string, env []string, dataDir, dbPath, serverURL string) func() string {
	t.Helper()
	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out := bootScriptedServeOnce(t, bin, env, dataDir, dbPath, serverURL)
		if out.ready {
			return out.log
		}
		if out.diedEarly && attempt < maxAttempts {
			t.Logf("serve boot attempt %d exited early (retrying):\n%s", attempt, out.log())
			continue
		}
		t.Fatalf("contenox serve never became ready (attempt %d):\n%s", attempt, out.log())
	}
	return func() string { return "" }
}

func bootScriptedServeOnce(t *testing.T, bin string, env []string, dataDir, dbPath, serverURL string) serveBootOutcome {
	t.Helper()
	var log bytes.Buffer
	cmd := exec.Command(bin, "--data-dir", dataDir, "--db", dbPath, "serve")
	cmd.Env = env
	cmd.Stdout = &log
	cmd.Stderr = &log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	pid := cmd.Process.Pid
	exited := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(exited) }()
	logFn := func() string { return log.String() }
	kill := func() {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}

	healthURL := serverURL + "/health"
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			kill()
			return serveBootOutcome{log: logFn, diedEarly: true}
		default:
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code == http.StatusOK {
				t.Cleanup(kill)
				return serveBootOutcome{log: logFn, ready: true}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	kill()
	return serveBootOutcome{log: logFn}
}
