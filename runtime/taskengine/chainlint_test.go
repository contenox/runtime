package taskengine_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func lintChain(tasks ...taskengine.TaskDefinition) *taskengine.TaskChainDefinition {
	return &taskengine.TaskChainDefinition{ID: "lint-test", Tasks: tasks}
}

func branchTo(op taskengine.OperatorTerm, when, goto_ string) taskengine.TransitionBranch {
	return taskengine.TransitionBranch{Operator: op, When: when, Goto: goto_}
}

func TestUnit_LintChain_GoodChainPasses(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "chat", Handler: taskengine.HandleChatCompletion,
			Transition: taskengine.TaskTransition{
				OnFailure: "summarise",
				Branches: []taskengine.TransitionBranch{
					branchTo(taskengine.OpEquals, taskengine.TransitionToolCall, "run_tools"),
					branchTo(taskengine.OpDefault, "", taskengine.TermEnd),
				},
			},
		},
		taskengine.TaskDefinition{
			ID: "run_tools", Handler: taskengine.HandleExecuteToolCalls, InputVar: "chat",
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "chat")},
			},
		},
		taskengine.TaskDefinition{
			ID: "summarise", Handler: taskengine.HandleChatCompletion, InputVar: "previous_output",
			Print: "chat said: {{.chat}} (error: {{.chat_error}})",
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", taskengine.TermEnd)},
			},
		},
	)
	require.NoError(t, taskengine.LintChain(chain))
	require.NoError(t, taskengine.LintChain(chain, taskengine.DataTypeChatHistory))
	require.NoError(t, taskengine.LintChain(chain, taskengine.DataTypeString))
}

func TestUnit_LintChain_ImpossibleEdgeIsLoadError(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "extract", Handler: taskengine.HandleNoop,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpEquals, taskengine.TransitionNoop, "summarize")},
			},
		},
		taskengine.TaskDefinition{
			ID: "summarize", Handler: taskengine.HandleExecuteToolCalls,
		},
	)
	err := taskengine.LintChain(chain, taskengine.DataTypeString)
	require.Error(t, err)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(),
		"task[summarize] handler execute_tool_calls cannot accept input from task[extract] (produces string; accepts chat_history)")
}

func TestUnit_LintChain_AnyFlowsStayRuntimeChecked(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "fetch", Handler: taskengine.HandleTools,
			Tools: &taskengine.ToolsCall{Name: "webtools", ToolName: "web_get"},
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "digest")},
			},
		},
		taskengine.TaskDefinition{ID: "digest", Handler: taskengine.HandleExecuteToolCalls},
	)
	require.NoError(t, taskengine.LintChain(chain, taskengine.DataTypeString))

	chain2 := lintChain(taskengine.TaskDefinition{ID: "digest", Handler: taskengine.HandleExecuteToolCalls})
	require.NoError(t, taskengine.LintChain(chain2))
}

func TestUnit_LintChain_OutputTemplateNarrowsToolsToString(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "fetch", Handler: taskengine.HandleTools,
			Tools:          &taskengine.ToolsCall{Name: "webtools", ToolName: "web_get"},
			OutputTemplate: "status: {{.status}}",
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "digest")},
			},
		},
		taskengine.TaskDefinition{ID: "digest", Handler: taskengine.HandleExecuteToolCalls},
	)
	err := taskengine.LintChain(chain, taskengine.DataTypeString)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(),
		"task[digest] handler execute_tool_calls cannot accept input from task[fetch] (produces string; accepts chat_history)")
}

func TestUnit_LintChain_PromptTemplateIntoExecuteToolCalls(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "run_tools", Handler: taskengine.HandleExecuteToolCalls,
			PromptTemplate: "{{.input}}",
		},
	)
	err := taskengine.LintChain(chain, taskengine.DataTypeChatHistory)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), "cannot take a prompt_template")
}

func TestUnit_LintChain_InputVarNeverProduced(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "chat", Handler: taskengine.HandleChatCompletion,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "next")},
			},
		},
		taskengine.TaskDefinition{
			ID: "next", Handler: taskengine.HandleChatCompletion, InputVar: "chatt",
		},
	)
	err := taskengine.LintChain(chain)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `input_var "chatt" is never produced by any task that can run before it`)
	require.Contains(t, err.Error(), "variables available here: chat, input, previous_output")
}

func TestUnit_LintChain_InputVarTypeMismatch(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "start", Handler: taskengine.HandleNoop,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "chat")},
			},
		},
		taskengine.TaskDefinition{
			ID: "chat", Handler: taskengine.HandleChatCompletion, InputVar: "input",
		},
	)
	err := taskengine.LintChain(chain, taskengine.DataTypeInt)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(),
		`task[chat] handler chat_completion cannot accept input from input_var "input" (produces int; accepts string, chat_history)`)
}

func TestUnit_LintChain_DeadBranchOnClosedVocabulary(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "chat", Handler: taskengine.HandleChatCompletion,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{
					branchTo(taskengine.OpEquals, "tool-call", "run_tools"),
					branchTo(taskengine.OpDefault, "", taskengine.TermEnd),
				},
			},
		},
		taskengine.TaskDefinition{ID: "run_tools", Handler: taskengine.HandleExecuteToolCalls, InputVar: "chat"},
	)
	err := taskengine.LintChain(chain)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `branch (equals "tool-call") can never fire`)
	require.Contains(t, err.Error(), "tool_call, executed")
}

func TestUnit_LintChain_RouteNeedsEqualsBranches(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "classify", Handler: taskengine.HandleRoute,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", taskengine.TermEnd)},
			},
		},
	)
	err := taskengine.LintChain(chain)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), "a route task needs at least one equals branch")
}

func TestUnit_LintChain_MacroChecks(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "chat", Handler: taskengine.HandleChatCompletion,
			SystemInstruction: "Used {{edge_count:chat->run_toolz}} rounds; {{edge_count:chat->end}}; model {{var:}}",
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{
					branchTo(taskengine.OpEquals, taskengine.TransitionToolCall, "run_tools"),
					branchTo(taskengine.OpDefault, "", taskengine.TermEnd),
				},
			},
		},
		taskengine.TaskDefinition{ID: "run_tools", Handler: taskengine.HandleExecuteToolCalls, InputVar: "chat"},
	)
	err := taskengine.LintChain(chain)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `{{edge_count:chat->run_toolz}} references unknown task "run_toolz"`)
	require.Contains(t, err.Error(), "counts an edge into 'end', which is never incremented")
	require.Contains(t, err.Error(), "{{var:}} names no variable")
}

func TestUnit_LintChain_TemplateRefToUnknownVar(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "report", Handler: taskengine.HandleChatCompletion,
			PromptTemplate: "summarise {{.summry}} briefly",
		},
	)
	err := taskengine.LintChain(chain)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), "references {{.summry}}, but no task or engine variable with that name exists")
}

func TestUnit_LintChain_StructuralDefectsWrapErrChainLint(t *testing.T) {
	dup := lintChain(
		taskengine.TaskDefinition{ID: "a", Handler: taskengine.HandleNoop},
		taskengine.TaskDefinition{ID: "a", Handler: taskengine.HandleNoop},
	)
	err := taskengine.LintChain(dup)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `duplicate task ID "a"`)

	badGoto := lintChain(
		taskengine.TaskDefinition{
			ID: "a", Handler: taskengine.HandleNoop,
			Transition: taskengine.TaskTransition{
				Branches: []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "gone")},
			},
		},
	)
	err = taskengine.LintChain(badGoto)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `goto references unknown task "gone"`)

	badHandler := lintChain(taskengine.TaskDefinition{ID: "a", Handler: "prompt"})
	err = taskengine.LintChain(badHandler)
	require.ErrorIs(t, err, taskengine.ErrChainLint)
	require.Contains(t, err.Error(), `unknown handler "prompt"`)
}

func TestUnit_LintChain_RaiseErrorFlowsOnlyThroughOnFailure(t *testing.T) {
	chain := lintChain(
		taskengine.TaskDefinition{
			ID: "fail", Handler: taskengine.HandleRaiseError,
			Transition: taskengine.TaskTransition{
				OnFailure: "explain",
				Branches:  []taskengine.TransitionBranch{branchTo(taskengine.OpDefault, "", "explain")},
			},
		},
		taskengine.TaskDefinition{ID: "explain", Handler: taskengine.HandleChatCompletion, InputVar: "last_error"},
	)
	require.NoError(t, taskengine.LintChain(chain, taskengine.DataTypeString))
}
