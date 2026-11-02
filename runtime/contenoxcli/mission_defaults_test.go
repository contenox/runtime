package contenoxcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/chainagents"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func missionDefaultsDB(t *testing.T) (context.Context, libdb.DBManager, runtimetypes.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "mission-defaults.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db, runtimetypes.New(db.WithoutTransaction())
}

func TestUnit_ReviewerDeclaration_CompilesToAReadOnlyMissionAgent(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	chain, err := agentdecl.CompileShippedAgent(cfg, DefaultMissionAgent)
	require.NoError(t, err)
	require.Equal(t, "reviewer", chain.ID)
	require.NoError(t, taskengine.LintChain(chain))
	require.True(t, chainagents.ChainGrantsMissionTools(chain))

	allowed := map[string]bool{"mission": true, "local_fs": true, "native-git": true}
	sawLoop := false
	for _, task := range chain.Tasks {
		require.NotNil(t, task.ExecuteConfig, "task %s", task.ID)
		if len(task.ExecuteConfig.Tools) == 0 {
			continue
		}
		for _, tool := range task.ExecuteConfig.Tools {
			require.Truef(t, allowed[tool], "task %s grants %q", task.ID, tool)
		}
		for _, hidden := range []string{"local_fs.write_file", "local_fs.edit_file", "local_fs.sed", "native-git.git_add", "native-git.git_commit", "native-git.git_checkout_branch", "native-git.git_restore"} {
			require.Containsf(t, task.ExecuteConfig.HideTools, hidden, "task %s must hide %s", task.ID, hidden)
		}
		if task.Handler == taskengine.HandleChatCompletion {
			sawLoop = true
			require.Contains(t, task.SystemInstruction, "{{date}}")
			require.Contains(t, task.SystemInstruction, "mission_report")
			require.Contains(t, task.SystemInstruction, "mission_finish")
			require.Contains(t, task.SystemInstruction, "NEVER EDIT FILES")
			require.Contains(t, task.SystemInstruction, "`landed`")
			require.NotContains(t, task.SystemInstruction, "{{rounds_used}}", "loop macros are expanded at compile time")
		}
	}
	require.True(t, sawLoop)
}

func TestUnit_PlannerDeclaration_CompilesToAMissionOnlyAgent(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	chain, err := agentdecl.CompileShippedAgent(cfg, "planner")
	require.NoError(t, err)
	require.Equal(t, "planner", chain.ID)
	require.NoError(t, taskengine.LintChain(chain))
	require.True(t, chainagents.ChainGrantsMissionTools(chain))
	for _, task := range chain.Tasks {
		if task.ExecuteConfig == nil || len(task.ExecuteConfig.Tools) == 0 {
			continue
		}
		require.Equalf(t, []string{"mission"}, task.ExecuteConfig.Tools, "task %s plans; it executes nothing", task.ID)
	}
}

func TestUnit_ShippedMissionAgents_AreDiscoveredFromTheirDeclarations(t *testing.T) {
	isolateHome(t)
	ctx, db, _ := missionDefaultsDB(t)
	dataDir := t.TempDir()
	require.NoError(t, seedAgentDeclarations(ctx, dataDir))

	agents := agentregistryservice.New(db)
	_, err := chainagents.Discover(ctx, agents, filepath.Join(dataDir, agentdecl.GeneratedDirName), dataDir)
	require.NoError(t, err)

	for _, name := range []string{"reviewer", "planner"} {
		agent, err := agents.GetByName(ctx, name)
		require.NoErrorf(t, err, "%s is registered from agents/%s.md", name, name)
		require.True(t, agent.Enabled)
		cfg, err := agent.ChainConfig()
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dataDir, agentdecl.GeneratedDirName, agentdecl.ChainFileFor(name)), cfg.Path)
		require.Truef(t, chainagents.MissionCapable(agent), "%s can run missions", name)
	}
	for _, name := range []string{"chain-acp", "chain-acpx"} {
		agent, err := agents.GetByName(ctx, name)
		require.NoErrorf(t, err, "%s is registered", name)
		require.Falsef(t, chainagents.MissionCapable(agent), "%s is a chat chain, not a mission agent", name)
	}
	_, err = agents.GetByName(ctx, "chain-contenox")
	require.ErrorIs(t, err, libdb.ErrNotFound, "the CLI chat chain compiles from agents/contenox/ but stays a utility chain")
}

func TestUnit_RetireSeededAgentChains_RemovesOnlyUntouchedShippedCopies(t *testing.T) {
	dir := t.TempDir()
	reviewer := filepath.Join(dir, "agent-reviewer.json")
	planner := filepath.Join(dir, "agent-planner.json")
	shipped, err := os.ReadFile(filepath.Join("testdata", "retired-agent-reviewer.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(reviewer, shipped, 0o600))
	require.NoError(t, os.WriteFile(planner, []byte(`{"id":"agent-planner","tasks":[]}`), 0o600))

	removed, err := retireSeededAgentChains(dir)
	require.NoError(t, err)
	require.Equal(t, []string{reviewer}, removed)
	_, err = os.Stat(reviewer)
	require.True(t, os.IsNotExist(err))
	got, err := os.ReadFile(planner)
	require.NoError(t, err)
	require.Equal(t, `{"id":"agent-planner","tasks":[]}`, string(got), "an edited copy is the operator's")
}

func TestUnit_MigrateRetiredMissionAgent_MovesOnlyRetiredNames(t *testing.T) {
	ctx, _, store := missionDefaultsDB(t)

	migrated, err := migrateRetiredMissionAgent(ctx, store)
	require.NoError(t, err)
	require.Empty(t, migrated, "nothing set, nothing to move")

	require.NoError(t, clikv.SetString(ctx, store, "default-mission-agent", "agent-reviewer"))
	migrated, err = migrateRetiredMissionAgent(ctx, store)
	require.NoError(t, err)
	require.Equal(t, "default-mission-agent=reviewer", migrated)
	require.Equal(t, "reviewer", clikv.Read(ctx, store, "default-mission-agent"))

	require.NoError(t, clikv.SetString(ctx, store, "default-mission-agent", "my-agent"))
	migrated, err = migrateRetiredMissionAgent(ctx, store)
	require.NoError(t, err)
	require.Empty(t, migrated)
	require.Equal(t, "my-agent", clikv.Read(ctx, store, "default-mission-agent"))
}

func TestUnit_SeedMissionDefaults_SetsOnlyUnsetKeys(t *testing.T) {
	ctx, _, store := missionDefaultsDB(t)

	seeded, err := seedMissionDefaults(ctx, store, missionDefaultCheck{})
	require.NoError(t, err)
	require.Equal(t, []string{"default-mission-agent=reviewer", "default-mission-policy=read_only"}, seeded)
	require.Equal(t, "reviewer", clikv.Read(ctx, store, "default-mission-agent"))
	require.Equal(t, "read_only", clikv.Read(ctx, store, "default-mission-policy"))

	require.NoError(t, clikv.SetString(ctx, store, "default-mission-agent", "my-agent"))
	require.NoError(t, clikv.SetString(ctx, store, "default-mission-policy", ""))
	seeded, err = seedMissionDefaults(ctx, store, missionDefaultCheck{})
	require.NoError(t, err)
	require.Empty(t, seeded)
	require.Equal(t, "my-agent", clikv.Read(ctx, store, "default-mission-agent"))
	require.Equal(t, "", clikv.Read(ctx, store, "default-mission-policy"))
}

func TestUnit_SeedMissionDefaults_SkipsValuesThatDoNotResolve(t *testing.T) {
	ctx, _, store := missionDefaultsDB(t)
	seeded, err := seedMissionDefaults(ctx, store, missionDefaultCheck{
		agent:  func(context.Context, string) bool { return false },
		policy: func(context.Context, string) bool { return true },
	})
	require.NoError(t, err)
	require.Equal(t, []string{"default-mission-policy=read_only"}, seeded)
	require.Equal(t, "", clikv.Read(ctx, store, "default-mission-agent"))
}

func TestUnit_DefaultMissionPolicy_ResolvesToTheRenderedReadOnlyEnvelope(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	require.NoError(t, seedAgentDeclarations(context.Background(), dataDir))

	validator := hitlservice.NewPolicyValidator(hitlPolicySource(dataDir), runtimetypes.LocalTenantID, "")
	require.NoError(t, validator.ValidatePolicy(context.Background(), DefaultMissionPolicy))
}
