package agentdecl_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type captureEnv struct {
	chain *taskengine.TaskChainDefinition
}

func (c *captureEnv) ExecEnv(_ context.Context, chain *taskengine.TaskChainDefinition, input any, dt taskengine.DataType) (any, taskengine.DataType, []taskengine.CapturedStateUnit, error) {
	c.chain = chain
	return input, dt, nil, nil
}

func TestUnit_SeededAgentPromptsRenderTheUserLocale(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	root, err := agentdecl.LocalRoot(filepath.Join("preseed", "agents", "acp"))
	require.NoError(t, err)
	tree, err := agentdecl.LoadTree(t.Context(), root, cfg)
	require.NoError(t, err)
	compiled, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	withMacro := 0
	for _, task := range compiled.Tasks {
		if strings.Contains(task.SystemInstruction, "{{var:locale|en-US}}") {
			withMacro++
		}
	}
	require.GreaterOrEqual(t, withMacro, 3, "coding, general and review agents carry the locale line")

	inner := &captureEnv{}
	env, err := taskengine.NewMacroEnv(inner, nil)
	require.NoError(t, err)
	ctx := taskengine.WithTemplateVars(libtracker.WithNewRequestID(context.Background()), map[string]string{
		"model":            "m",
		"provider":         "p",
		"default_model":    "m",
		"default_provider": "p",
		"think":            "low",
		"locale":           "de-DE",
	})
	_, _, _, err = env.ExecEnv(ctx, compiled, "", taskengine.DataTypeString)
	require.NoError(t, err)
	require.NotNil(t, inner.chain)

	rendered := 0
	for _, task := range inner.chain.Tasks {
		if !strings.Contains(task.SystemInstruction, "locale is de-DE") {
			continue
		}
		rendered++
		require.NotContains(t, task.SystemInstruction, "{{var:locale")
	}
	require.Equal(t, withMacro, rendered)
}
