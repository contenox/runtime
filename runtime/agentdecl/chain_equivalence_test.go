package agentdecl_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadCurrentACPChain(t *testing.T) *taskengine.TaskChainDefinition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "contenoxcli", "chain-acp.json"))
	require.NoError(t, err)
	var chain taskengine.TaskChainDefinition
	require.NoError(t, json.Unmarshal(data, &chain))
	return &chain
}

func loopBudget(chain *taskengine.TaskChainDefinition, taskID string) string {
	for _, task := range chain.Tasks {
		if task.ID != taskID {
			continue
		}
		for _, branch := range task.Transition.Branches {
			if branch.Operator == taskengine.OpEdgeTraversedAtLeast {
				return branch.When
			}
		}
	}
	return ""
}

func equivalenceTaskByID(chain *taskengine.TaskChainDefinition, id string) *taskengine.TaskDefinition {
	for i := range chain.Tasks {
		if chain.Tasks[i].ID == id {
			return &chain.Tasks[i]
		}
	}
	return nil
}

func TestUnit_ChainEquivalence_CompiledACPTreeMatchesTheHandWrittenChain(t *testing.T) {
	current := loadCurrentACPChain(t)
	codingBudget := loopBudget(current, "coding_chat")
	recoveryBudget := loopBudget(current, "coding_recovery")
	require.Equal(t, "12", codingBudget)
	require.Equal(t, "8", recoveryBudget)

	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	root, err := agentdecl.LocalRoot(filepath.Join("preseed", "agents", "acp"))
	require.NoError(t, err)
	tree, err := agentdecl.LoadTree(t.Context(), root, cfg)
	require.NoError(t, err)
	compiled, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	var router *taskengine.TaskDefinition
	for i := range compiled.Tasks {
		if compiled.Tasks[i].Handler == taskengine.HandleRoute {
			router = &compiled.Tasks[i]
			break
		}
	}
	require.NotNil(t, router, "the compiled tree opens with a router")
	require.GreaterOrEqual(t, len(router.Transition.Branches), 4, "three destinations plus a default")
	for _, branch := range router.Transition.Branches {
		assert.NotEmpty(t, equivalenceTaskByID(compiled, branch.Goto), "router target %q exists", branch.Goto)
	}

	var agentTasks, recoveryTasks int
	for i := range compiled.Tasks {
		task := &compiled.Tasks[i]
		switch {
		case task.Handler == taskengine.HandleChatCompletion && strings.HasSuffix(task.ID, "-agent"):
			agentTasks++
			assert.Equal(t, codingBudget, loopBudget(compiled, task.ID), "%s keeps the main-loop budget", task.ID)
			require.NotNil(t, task.ExecuteConfig)
			assert.Contains(t, task.ExecuteConfig.Tools, "*", "%s inherits every toolset, as the hand-written loop does", task.ID)
			assert.False(t, task.ExecuteConfig.PassClientsTools)

			var toolCall *taskengine.TransitionBranch
			for j := range task.Transition.Branches {
				if task.Transition.Branches[j].Operator == taskengine.OpEquals &&
					task.Transition.Branches[j].When == "tool_call" {
					toolCall = &task.Transition.Branches[j]
				}
			}
			require.NotNil(t, toolCall, "%s hands tool calls on", task.ID)
			assert.NotNil(t, equivalenceTaskByID(compiled, toolCall.Goto), "tool target %q exists", toolCall.Goto)
			assert.True(t, strings.HasSuffix(toolCall.Goto, "-tools"))
			assert.NotEmpty(t, task.Transition.OnFailure, "a failed turn leaves the loop")
			assert.NotNil(t, equivalenceTaskByID(compiled, task.Transition.OnFailure), "failure target %q exists", task.Transition.OnFailure)

		case task.Handler == taskengine.HandleChatCompletion && strings.HasSuffix(task.ID, "-recovery"):
			recoveryTasks++
			assert.Equal(t, recoveryBudget, loopBudget(compiled, task.ID), "%s keeps the recovery budget", task.ID)
		}
	}
	assert.Equal(t, 3, agentTasks, "coding, general and review each keep a loop")
	assert.GreaterOrEqual(t, recoveryTasks, 2, "the tree keeps recovery loops where the chain had them")

	var terminal *taskengine.TaskDefinition
	for i := range compiled.Tasks {
		if strings.HasSuffix(compiled.Tasks[i].ID, "-summarise") {
			terminal = &compiled.Tasks[i]
		}
	}
	require.NotNil(t, terminal, "the compiled tree ends in a failure summary")
	assert.Equal(t, taskengine.HandleChatCompletion, terminal.Handler)
	require.NotNil(t, terminal.ExecuteConfig)
	assert.Empty(t, terminal.ExecuteConfig.Tools, "the summary task has no tools")
	assert.False(t, terminal.ExecuteConfig.PassClientsTools)

	coding := equivalenceTaskByID(compiled, "chain-acp-coding-agent")
	require.NotNil(t, coding)
	assert.Contains(t, coding.ExecuteConfig.ToolsPolicies, "local_shell")
	assert.Contains(t, coding.ExecuteConfig.ToolsPolicies, "local_fs")
}

func TestUnit_CompiledPrompts_LeaveNoMacroUnexpanded(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	root, err := agentdecl.LocalRoot(filepath.Join("preseed", "agents", "acp"))
	require.NoError(t, err)
	tree, err := agentdecl.LoadTree(t.Context(), root, cfg)
	require.NoError(t, err)
	compiled, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	known := []string{"{{date}}", "{{now}}", "{{tools}}", "{{workspace:root}}", "{{host:os}}", "{{host:arch}}", "{{host:shell}}"}
	for _, task := range compiled.Tasks {
		prompt := task.SystemInstruction
		if prompt == "" {
			continue
		}
		rest := prompt
		for _, macro := range known {
			rest = strings.ReplaceAll(rest, macro, "")
		}
		for _, macro := range []string{"{{var:cwd", "{{var:model", "{{var:provider", "{{var:max_tokens", "{{edge_count:"} {
			rest = strings.ReplaceAll(rest, macro, "")
			_ = macro
		}
		assert.NotContains(t, rest, "{{", "%s carries an unexpanded macro: %s", task.ID, prompt)
	}
}
