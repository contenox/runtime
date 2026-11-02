package contenoxcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
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

func TestUnit_ReviewerChain_ParsesAndIsReadOnlyMissionAgent(t *testing.T) {
	var chain taskengine.TaskChainDefinition
	require.NoError(t, json.Unmarshal([]byte(initReviewerChain), &chain))
	require.Equal(t, "agent-reviewer", chain.ID)
	require.NoError(t, taskengine.LintChain(&chain))
	require.True(t, chainagents.ChainGrantsMissionTools(&chain))

	ids := map[string]bool{}
	for _, task := range chain.Tasks {
		ids[task.ID] = true
	}
	allowed := map[string]bool{"mission": true, "local_fs": true, "native-git": true}
	sawLoop := false
	for _, task := range chain.Tasks {
		require.NotNil(t, task.ExecuteConfig, "task %s", task.ID)
		require.NotEmpty(t, task.ExecuteConfig.Tools, "task %s", task.ID)
		for _, tool := range task.ExecuteConfig.Tools {
			require.Truef(t, allowed[tool], "task %s grants %q", task.ID, tool)
		}
		for _, hidden := range []string{"local_fs.write_file", "local_fs.sed", "native-git.git_add", "native-git.git_commit", "native-git.git_checkout_branch", "native-git.git_restore"} {
			require.Containsf(t, task.ExecuteConfig.HideTools, hidden, "task %s must hide %s", task.ID, hidden)
		}
		if task.Handler == taskengine.HandleChatCompletion {
			require.Contains(t, task.SystemInstruction, "{{date}}")
			require.Contains(t, []string{"{{var:model}}", "{{var:alt_model|var:model}}"}, task.ExecuteConfig.Model)
			require.Contains(t, task.SystemInstruction, "mission_report")
			require.Contains(t, task.SystemInstruction, "mission_finish")
			require.Contains(t, task.SystemInstruction, "NEVER EDIT FILES")
		}
		if task.ID == "review_loop" {
			sawLoop = true
			require.Equal(t, "{{var:model}}", task.ExecuteConfig.Model)
			require.Equal(t, "{{var:provider}}", task.ExecuteConfig.Provider)
			require.Equal(t, "review_recovery", task.Transition.OnFailure)
			require.Contains(t, task.SystemInstruction, "`result`")
			require.Contains(t, task.SystemInstruction, "`landed`")
			require.Contains(t, task.SystemInstruction, "`stuck`")
			require.Contains(t, task.SystemInstruction, "`derailed`")
		}
		for _, br := range task.Transition.Branches {
			if br.Goto != "end" {
				require.Truef(t, ids[br.Goto], "task %s branches to unknown task %q", task.ID, br.Goto)
			}
		}
	}
	require.True(t, sawLoop)
}

func TestUnit_SeedMissionAgentChains_DiscoveryRegistersReviewerAndPlanner(t *testing.T) {
	ctx, db, _ := missionDefaultsDB(t)
	dataDir := t.TempDir()

	created, err := seedMissionAgentChains(dataDir)
	require.NoError(t, err)
	require.Len(t, created, 2)

	agents := agentregistryservice.New(db)
	res, err := chainagents.Discover(ctx, agents, dataDir)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"agent-reviewer", "agent-planner"}, res.Created)

	reviewer, err := agents.GetByName(ctx, "agent-reviewer")
	require.NoError(t, err)
	require.True(t, reviewer.Enabled)
	cfg, err := reviewer.ChainConfig()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dataDir, "agent-reviewer.json"), cfg.Path)
	require.True(t, chainagents.MissionCapable(reviewer))
}

func TestUnit_SeedMissionAgentChains_NeverOverwrites(t *testing.T) {
	dataDir := t.TempDir()
	custom := filepath.Join(dataDir, "agent-reviewer.json")
	require.NoError(t, os.WriteFile(custom, []byte(`{"id":"agent-reviewer","tasks":[]}`), 0o600))

	created, err := seedMissionAgentChains(dataDir)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dataDir, "agent-planner.json")}, created)

	got, err := os.ReadFile(custom)
	require.NoError(t, err)
	require.Equal(t, `{"id":"agent-reviewer","tasks":[]}`, string(got))
}

func TestUnit_SeedMissionDefaults_SetsOnlyUnsetKeys(t *testing.T) {
	ctx, _, store := missionDefaultsDB(t)

	seeded, err := seedMissionDefaults(ctx, store, missionDefaultCheck{})
	require.NoError(t, err)
	require.Equal(t, []string{"default-mission-agent=agent-reviewer", "default-mission-policy=read_only"}, seeded)
	require.Equal(t, "agent-reviewer", clikv.Read(ctx, store, "default-mission-agent"))
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
