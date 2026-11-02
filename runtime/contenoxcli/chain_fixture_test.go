package contenoxcli

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

var shippedInteractiveAgents = []string{"acp", "acpx", "contenox", "run"}

func compiledShippedAgent(t *testing.T, name string) *taskengine.TaskChainDefinition {
	t.Helper()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	chain, err := agentdecl.CompileShippedAgent(cfg, name)
	require.NoErrorf(t, err, "agents/%s compiles", name)
	require.NoErrorf(t, taskengine.LintChain(chain), "agents/%s lints", name)
	return chain
}

func embeddedSystemChain(t *testing.T, raw string) *taskengine.TaskChainDefinition {
	t.Helper()
	var chain taskengine.TaskChainDefinition
	require.NoError(t, json.Unmarshal([]byte(raw), &chain))
	return &chain
}

func TestUnit_ShippedAgents_SetThinkOnlyOnChatTasks(t *testing.T) {
	for _, name := range shippedInteractiveAgents {
		t.Run(name, func(t *testing.T) {
			for _, task := range compiledShippedAgent(t, name).Tasks {
				require.NotNil(t, task.ExecuteConfig, "task %s execute_config", task.ID)
				switch task.Handler {
				case taskengine.HandleChatCompletion:
					require.Equal(t, "{{var:think}}", task.ExecuteConfig.Think, "task %s think", task.ID)
				default:
					require.Empty(t, task.ExecuteConfig.Think, "task %s should not set think", task.ID)
				}
			}
		})
	}
	for _, task := range embeddedSystemChain(t, initCompactChain).Tasks {
		require.Empty(t, task.ExecuteConfig.Think, "compaction %s should not set think", task.ID)
	}
}

func TestUnit_RoutedAgents_RouteToBoundedLoopsWithAFailureSummary(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	for _, name := range []string{"acp", "contenox"} {
		t.Run(name, func(t *testing.T) {
			chain := compiledShippedAgent(t, name)
			byID := map[string]taskengine.TaskDefinition{}
			for _, task := range chain.Tasks {
				byID[task.ID] = task
			}
			router := chain.Tasks[0]
			require.Equal(t, taskengine.HandleRoute, router.Handler, "the chain opens with its router")
			labels := map[string]bool{}
			for _, branch := range router.Transition.Branches {
				require.Containsf(t, byID, branch.Goto, "router target %q exists", branch.Goto)
				if branch.Operator == taskengine.OpEquals {
					labels[branch.When] = true
				}
			}
			require.True(t, labels["coding"], "routes code changes to the coding loop")
			require.True(t, labels["general"], "routes everything else to the general loop")

			var summary *taskengine.TaskDefinition
			for i := range chain.Tasks {
				task := chain.Tasks[i]
				if task.Handler != taskengine.HandleChatCompletion {
					continue
				}
				if len(task.ExecuteConfig.Tools) == 0 {
					summary = &chain.Tasks[i]
					continue
				}
				if task.InputVar != "" {
					continue
				}
				tools := branchGotoOp(t, task, taskengine.OpEquals, taskengine.TransitionToolCall)
				require.Equal(t, taskengine.HandleExecuteToolCalls, byID[tools.Goto].Handler)
				require.Equal(t, task.ID, byID[tools.Goto].InputVar)
				budget := branchGotoOp(t, task, taskengine.OpEdgeTraversedAtLeast, "")
				require.Equalf(t, itoa(cfg.Chain.MainRounds), budget.When, "task %s keeps the main-loop budget", task.ID)
				require.Equal(t, taskengine.TermEnd, branchGotoOp(t, task, taskengine.OpDefault, "").Goto)
			}
			require.NotNil(t, summary, "a failed turn ends in a failure summary")
			require.Empty(t, summary.ExecuteConfig.Tools)
		})
	}
}

func TestUnit_ShippedAgents_RecoveryUsesConfiguredDefaultFallback(t *testing.T) {
	for _, name := range shippedInteractiveAgents {
		t.Run(name, func(t *testing.T) {
			saw := false
			for _, task := range compiledShippedAgent(t, name).Tasks {
				if task.Handler != taskengine.HandleChatCompletion || task.InputVar == "" {
					continue
				}
				saw = true
				require.Equal(t, "{{var:alt_model|var:default_model}}", task.ExecuteConfig.Model, "task %s model", task.ID)
				require.Equal(t, "{{var:alt_provider|var:default_provider}}", task.ExecuteConfig.Provider, "task %s provider", task.ID)
			}
			require.True(t, saw, "a recovery or summary task exists")
		})
	}
}

func TestUnit_ShippedAgents_UseConservativeToolOutputCaps(t *testing.T) {
	for _, name := range shippedInteractiveAgents {
		t.Run(name, func(t *testing.T) {
			for _, task := range compiledShippedAgent(t, name).Tasks {
				if fsPolicy := task.ExecuteConfig.ToolsPolicies["local_fs"]; len(fsPolicy) > 0 {
					require.Equal(t, "262144", fsPolicy["_max_read_bytes"], "task %s", task.ID)
					require.Equal(t, "131072", fsPolicy["_max_output_bytes"], "task %s", task.ID)
					require.Equal(t, "1000", fsPolicy["_max_grep_matches"], "task %s", task.ID)
				}
				if webPolicy := task.ExecuteConfig.ToolsPolicies["webtools"]; len(webPolicy) > 0 {
					require.Equal(t, "262144", webPolicy["_max_response_bytes"], "task %s", task.ID)
				}
			}
		})
	}
}

func TestUnit_ShippedAgents_ScopeToolExecutionNodes(t *testing.T) {
	for _, name := range shippedInteractiveAgents {
		t.Run(name, func(t *testing.T) {
			for _, task := range compiledShippedAgent(t, name).Tasks {
				if task.Handler != taskengine.HandleExecuteToolCalls {
					continue
				}
				require.Contains(t, task.ExecuteConfig.Tools, "*", "task %s tools", task.ID)
				require.Contains(t, task.ExecuteConfig.ToolsPolicies, "local_fs", "task %s", task.ID)
				require.Contains(t, task.ExecuteConfig.ToolsPolicies, "webtools", "task %s", task.ID)
			}
		})
	}
}

func TestUnit_ShippedChains_LLMTasksIncludeDateMacro(t *testing.T) {
	chains := map[string]*taskengine.TaskChainDefinition{
		"compact": embeddedSystemChain(t, initCompactChain),
		"fim":     embeddedSystemChain(t, initFIMChain),
	}
	for _, name := range shippedInteractiveAgents {
		chains[name] = compiledShippedAgent(t, name)
	}
	for name, chain := range chains {
		for _, task := range chain.Tasks {
			switch task.Handler {
			case taskengine.HandleChatCompletion, taskengine.HandleRoute:
				require.Contains(t, task.SystemInstruction, "{{date}}", "%s/%s", name, task.ID)
			}
		}
	}
}

func TestUnit_PlannerDeclaration_KeepsItsPlanDiscipline(t *testing.T) {
	saw := false
	for _, task := range compiledShippedAgent(t, "planner").Tasks {
		if task.Handler != taskengine.HandleChatCompletion || len(task.ExecuteConfig.Tools) == 0 {
			continue
		}
		saw = true
		for _, marker := range []string{
			"{{date}}",
			"FULL SNAPSHOT",
			"echoing the `id`",
			"in_progress at any time",
			"pending to completed",
			"explanation",
			"NEVER RESTATE THE PLAN",
			"mission_report",
			"handover",
			"mission_finish",
			"not yet yours",
		} {
			require.Containsf(t, task.SystemInstruction, marker, "planner task %s is missing the %q discipline", task.ID, marker)
		}
	}
	require.True(t, saw)
}

func TestUnit_ShippedChains_ModelMacroFallbacksAlwaysSeeded(t *testing.T) {
	alwaysSeeded := map[string]bool{
		"model": true, "provider": true,
		"default_model": true, "default_provider": true,
	}
	chains := map[string]*taskengine.TaskChainDefinition{
		"compact": embeddedSystemChain(t, initCompactChain),
	}
	for _, name := range append(append([]string{}, shippedInteractiveAgents...), "reviewer", "planner") {
		chains[name] = compiledShippedAgent(t, name)
	}
	macroRe := regexp.MustCompile(`^\{\{var:([a-z_]+)(\|var:([a-z_]+))?\}\}$`)
	for name, chain := range chains {
		for _, task := range chain.Tasks {
			if task.ExecuteConfig == nil || task.ExecuteConfig.Model == "" {
				continue
			}
			m := macroRe.FindStringSubmatch(task.ExecuteConfig.Model)
			require.NotNil(t, m, "%s/%s: unexpected model macro shape %q", name, task.ID, task.ExecuteConfig.Model)
			final := m[1]
			if m[3] != "" {
				final = m[3]
			}
			require.True(t, alwaysSeeded[final],
				"%s/%s: model macro %q bottoms out in %q, which is not always seeded",
				name, task.ID, task.ExecuteConfig.Model, final)
		}
	}
}

func branchGotoOp(t *testing.T, task taskengine.TaskDefinition, operator taskengine.OperatorTerm, when string) taskengine.TransitionBranch {
	t.Helper()
	for _, branch := range task.Transition.Branches {
		if branch.Operator == operator && (when == "" || branch.When == when) {
			return branch
		}
	}
	require.Failf(t, "missing branch", "task %s missing branch operator=%s when=%q", task.ID, operator, when)
	return taskengine.TransitionBranch{}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
