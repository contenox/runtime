package externalagents_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/contenox/runtime/apiframework"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/agentoptions"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/externalagents"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func newService(t *testing.T, lookPath func(string) (string, error)) (*externalagents.Service, agentregistryservice.Service) {
	svc, agents, _ := newServiceWithDB(t, lookPath)
	return svc, agents
}

func newServiceWithDB(t *testing.T, lookPath func(string) (string, error)) (*externalagents.Service, agentregistryservice.Service, libdb.DBManager) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "external-agents.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	agents := agentregistryservice.New(db)
	return externalagents.New(externalagents.Deps{
		Agents:      agents,
		DB:          db,
		ContenoxDir: t.TempDir(),
		Home:        t.TempDir(),
		LookPath:    lookPath,
	}), agents, db
}

func TestUnit_ExternalAgents_CRUD(t *testing.T) {
	ctx := context.Background()
	svc, agents := newService(t, nil)

	created, err := svc.Create(ctx, externalagents.Input{
		Name:    "my-agent",
		Command: "my-agent-bin",
		Args:    []string{"--acp"},
		Env:     map[string]string{"FOO": "bar"},
		Paths:   []runtimetypes.ExternalACPPath{{Path: "~/.my-agent", Mode: "rw", Needs: "its sign-in"}},
		Modes:   []string{"ask"},
	})
	require.NoError(t, err)
	require.True(t, created.Enabled)
	require.Equal(t, []string{"--acp"}, created.Args)
	require.Empty(t, created.Preset)

	chain := &runtimetypes.Agent{Name: "a-chain", Enabled: true}
	require.NoError(t, chain.SetChainConfig(runtimetypes.ChainConfig{Path: filepath.Join(t.TempDir(), "c.json")}))
	require.NoError(t, agents.Create(ctx, chain))

	list, err := svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "chain agents are not external agents")
	require.Equal(t, "my-agent", list[0].Name)

	off := false
	updated, err := svc.Update(ctx, "my-agent", externalagents.Input{Command: "other-bin", Enabled: &off})
	require.NoError(t, err)
	require.Equal(t, "other-bin", updated.Command)
	require.False(t, updated.Enabled)
	require.Empty(t, updated.Args)

	enabled, err := svc.SetEnabled(ctx, "my-agent", true)
	require.NoError(t, err)
	require.True(t, enabled.Enabled)

	_, err = svc.Get(ctx, "a-chain")
	require.ErrorIs(t, err, apiframework.ErrNotFound)

	require.NoError(t, svc.Delete(ctx, "my-agent"))
	_, err = svc.Get(ctx, "my-agent")
	require.ErrorIs(t, err, apiframework.ErrNotFound)
}

func TestUnit_ExternalAgents_CreateValidates(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t, nil)

	_, err := svc.Create(ctx, externalagents.Input{Name: "Bad Name", Command: "x"})
	require.ErrorIs(t, err, apiframework.ErrInvalidParameterValue)

	_, err = svc.Create(ctx, externalagents.Input{Name: "ok"})
	require.ErrorIs(t, err, apiframework.ErrMissingParameter)

	_, err = svc.Create(ctx, externalagents.Input{Name: "ok", Command: "x", Paths: []runtimetypes.ExternalACPPath{{Path: "~/.x", Mode: "rw"}}})
	require.ErrorIs(t, err, apiframework.ErrInvalidParameterValue, "a writable path must say why the agent needs it")

	_, err = svc.Create(ctx, externalagents.Input{Name: "claude-code", Command: "x"})
	require.ErrorIs(t, err, apiframework.ErrConflict, "the preset's name is reserved")

	_, err = svc.Create(ctx, externalagents.Input{Name: "dup", Command: "x"})
	require.NoError(t, err)
	_, err = svc.Create(ctx, externalagents.Input{Name: "dup", Command: "x"})
	require.ErrorIs(t, err, apiframework.ErrConflict)
}

func TestUnit_ExternalAgents_InstallReportsMissingNode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t, func(string) (string, error) { return "", exec.ErrNotFound })

	presets, err := svc.Presets(ctx)
	require.NoError(t, err)
	require.Len(t, presets, 1)
	require.Equal(t, externalagents.PresetClaudeCode, presets[0].ID)
	require.False(t, presets[0].Installed)
	require.False(t, presets[0].Node.Found)

	_, err = svc.InstallPreset(ctx, externalagents.PresetClaudeCode)
	require.NoError(t, err)
	var st externalagents.PresetStatus
	require.Eventually(t, func() bool {
		all, err := svc.Presets(ctx)
		require.NoError(t, err)
		st = all[0]
		return st.InstallState != externalagents.InstallRunning
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, externalagents.InstallFailed, st.InstallState)
	require.NotNil(t, st.InstallProblem)
	require.Equal(t, externalagents.ProblemNodeMissing, st.InstallProblem.Kind)

	_, err = svc.InstallPreset(ctx, "nope")
	require.ErrorIs(t, err, apiframework.ErrNotFound)
}

func buildStubAgent(t *testing.T) string {
	t.Helper()
	name := "acp-stub-agent"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", bin, "github.com/contenox/runtime/libacp/cmd/acp-stub-agent").CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

func TestHost_ExternalAgents_CheckStartsAgentInSandbox(t *testing.T) {
	if err := libsandbox.Preflight(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	ctx := context.Background()
	svc, _ := newService(t, nil)
	_, err := svc.Create(ctx, externalagents.Input{Name: "stub", Command: buildStubAgent(t)})
	require.NoError(t, err)

	res, err := svc.Check(ctx, "stub")
	require.NoError(t, err)
	require.Nil(t, res.Problem, "%+v", res.Problem)
	require.True(t, res.Ready)
	require.NotNil(t, res.Agent)
	require.Equal(t, "acp-stub-agent", res.Agent.Name)
	require.Nil(t, res.SignedIn, "an agent that reports no sign-in state is not assumed signed out")

	_, err = svc.Create(ctx, externalagents.Input{Name: "missing", Command: filepath.Join(t.TempDir(), "does-not-exist")})
	require.NoError(t, err)
	res, err = svc.Check(ctx, "missing")
	require.NoError(t, err)
	require.NotNil(t, res.Problem)
	require.Equal(t, externalagents.ProblemCommandNotFound, res.Problem.Kind)

	_, err = svc.SetEnabled(ctx, "stub", false)
	require.NoError(t, err)
	res, err = svc.Check(ctx, "stub")
	require.NoError(t, err)
	require.Equal(t, externalagents.ProblemDisabled, res.Problem.Kind)

	_, err = svc.Check(ctx, "nobody")
	require.True(t, errors.Is(err, apiframework.ErrNotFound))
}

func TestHost_ExternalAgents_CheckRemembersSessionOptions(t *testing.T) {
	if err := libsandbox.Preflight(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	ctx := context.Background()
	svc, _, db := newServiceWithDB(t, nil)
	_, err := svc.Create(ctx, externalagents.Input{
		Name:    "stub",
		Command: buildStubAgent(t),
		Env:     map[string]string{"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1"},
	})
	require.NoError(t, err)

	res, err := svc.Check(ctx, "stub")
	require.NoError(t, err)
	require.Nil(t, res.Problem, "%+v", res.Problem)
	require.NotEmpty(t, agentoptions.Load(ctx, runtimetypes.New(db.WithoutTransaction()), "stub"),
		"the check opens a session so the first chat can show the agent's options before it starts")
}
