package agentdecl_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_PortSmoke_CompilesAShippedDeclaration(t *testing.T) {
	path := filepath.Join("preseed", "agents", "acp", "coding", "agent.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	ir, err := agentdecl.Parse(path, data, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, ir.Name)
	require.NotEmpty(t, ir.SystemPrompt, "the Markdown body is the system prompt")

	chain, err := agentdecl.EmitChain(ir, cfg)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(chain.ID, ir.Name), "chain id %q names the agent", chain.ID)
	require.GreaterOrEqual(t, len(chain.Tasks), 2)

	var chatTasks, toolTasks, bounded int
	for _, task := range chain.Tasks {
		switch task.Handler {
		case "chat_completion":
			chatTasks++
		case "execute_tool_calls":
			toolTasks++
		}
		for _, branch := range task.Transition.Branches {
			if branch.Operator == "edge_traversed_at_least" {
				bounded++
				assert.NotEmpty(t, branch.Edge)
				assert.NotEmpty(t, branch.When)
			}
		}
	}
	assert.Positive(t, chatTasks, "the emitted loop has a model turn")
	assert.Equal(t, 2, toolTasks, "tool calls run in their own tasks (main and recovery)")
	assert.Positive(t, bounded, "the loop is bounded by an edge counter")
}
func TestUnit_PortSmoke_LoadsAndEmitsTheACPTree(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	root, err := agentdecl.LocalRoot(filepath.Join("preseed", "agents", "acp"))
	require.NoError(t, err)

	tree, err := agentdecl.LoadTree(context.Background(), root, cfg)
	require.NoError(t, err)

	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, chain.Tasks)

	ids := make([]string, 0, len(chain.Tasks))
	for _, task := range chain.Tasks {
		ids = append(ids, task.ID)
	}
	joined := strings.Join(ids, ",")
	for _, branch := range []string{"coding", "general", "review"} {
		assert.Contains(t, joined, "-"+branch+"-agent", "the %s branch is emitted: %v", branch, ids)
		assert.Contains(t, joined, "-"+branch+"-tools", "%s tool task is emitted", branch)
	}
	assert.Contains(t, joined, "-route", "the tree root is a router: %v", ids)
	assert.Contains(t, joined, "-summarise", "the terminal task is emitted: %v", ids)
}
