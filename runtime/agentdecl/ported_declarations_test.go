package agentdecl_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func declarationBody(toolsLine string) []byte {
	return []byte("---\nname: probe\ndescription: probe\nposture: read_only\n" + toolsLine + "---\n\nDo the thing.\n")
}

func TestUnit_Declaration_ToolGrantsShapeTheEmittedChain(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	cases := []struct {
		name  string
		tools string
		want  []string
	}{
		{"omitted grants everything", "", []string{"*"}},
		{"a quoted star grants everything", "tools: \"*\"\n", []string{"*"}},
		{"named tools map to their toolsets", "tools: Read, Bash\n", []string{"local_fs", "local_shell"}},
		{"an empty list grants nothing", "tools: []\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ir, err := agentdecl.ParseAs(agentdecl.DialectClaudeCode, "home/agents/probe.md", declarationBody(tc.tools), cfg)
			require.NoError(t, err)
			chain, err := agentdecl.EmitChain(ir, cfg)
			require.NoError(t, err)

			var agentTask *int
			for i := range chain.Tasks {
				if chain.Tasks[i].Handler == "chat_completion" {
					agentTask = &i
					break
				}
			}
			require.NotNil(t, agentTask)
			got := chain.Tasks[*agentTask].ExecuteConfig.Tools
			for _, want := range tc.want {
				assert.Contains(t, got, want, "%s grants %s", tc.name, want)
			}
			if len(tc.want) == 0 {
				assert.NotContains(t, got, "local_fs", "an empty tools list grants no filesystem")
				assert.NotContains(t, got, "local_shell", "an empty tools list grants no shell")
			}
		})
	}
}

func TestUnit_EnvelopeName_AcceptsBareAndRenderedNames(t *testing.T) {
	for _, name := range []string{"default", "hitl-policy-default.json", "read_only", "hitl-policy-read_only.json"} {
		_, ok := agentdecl.EnvelopeName(name)
		assert.True(t, ok, "%q resolves to an envelope", name)
	}
	if got, ok := agentdecl.EnvelopeName("hitl-policy-default.json"); ok {
		assert.Equal(t, "default", got, "the rendered filename resolves to the bare name")
	}
	if got, ok := agentdecl.EnvelopeName("default"); ok {
		assert.Equal(t, "default", got)
	}
}

func TestUnit_Declaration_CategoryAndIconReachTheIR(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	data := []byte("---\nname: probe\ndescription: probe\nposture: read_only\ncategory: coding\nicon: code\n---\n\nBody.\n")
	ir, err := agentdecl.ParseAs(agentdecl.DialectClaudeCode, "home/agents/probe.md", data, cfg)
	require.NoError(t, err)
	assert.Equal(t, "coding", ir.Category)
	assert.Equal(t, "code", ir.Icon)
}
