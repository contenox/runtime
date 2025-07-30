package taskengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/activitytracker"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/stretchr/testify/require"
)

func TestUnit_SimpleEnv_ExecEnv_SingleTask(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "42",
		MockRawResponse: "42",
		MockError:       nil,
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(t.Context(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `What is {{.input}}?`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{
							Operator: "equals",
							When:     "42",
							Goto:     taskengine.TermEnd,
						},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "6 * 7", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "42", result)
}

func TestUnit_SimpleEnv_ExecEnv_FailsAfterRetries(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockError: errors.New("permanent failure"),
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `Broken task`,
				RetryOnFailure: 1,
				Transition:     taskengine.TaskTransition{},
			},
		},
	}

	_, _, err = env.ExecEnv(context.Background(), chain, "", taskengine.DataTypeString)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed after 1 retries")
}

func TestUnit_SimpleEnv_ExecEnv_TransitionsToNextTask(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "intermediate",
		MockRawResponse: "continue",
		MockError:       nil,
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `{{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "continue", Goto: "task2"},
					},
				},
			},
			{
				ID:             "task2",
				Type:           taskengine.RawString,
				PromptTemplate: `Follow up`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "continue", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "step one", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "intermediate", result)
}

func TestUnit_SimpleEnv_ExecEnv_ErrorTransition(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		ErrorSequence:   []error{errors.New("first failure"), nil},
		MockOutput:      "error recovered",
		MockRawResponse: "recovered",
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `fail`,
				Transition: taskengine.TaskTransition{
					OnFailure: "task2",
				},
			},
			{
				ID:             "task2",
				Type:           taskengine.RawString,
				PromptTemplate: `recover`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "recovered", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "oops", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "error recovered", result)
}

func TestUnit_SimpleEnv_ExecEnv_PrintTemplate(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "printed-value",
		MockRawResponse: "printed-value",
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `hi {{.input}}`,
				Print:          `Output: {{.task1}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "printed-value", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "user", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "printed-value", result)
}

func TestUnit_SimpleEnv_ExecEnv_InputVar_OriginalInput(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "processed: hello",
		MockRawResponse: "processed: hello",
		MockError:       nil,
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				InputVar:       "input", // Explicitly use original input
				PromptTemplate: `Process this: {{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "hello", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "processed: hello", result)
}

func TestUnit_SimpleEnv_ExecEnv_InputVar_PreviousTaskOutput(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutputSequence:      []any{"42", "processed: 42"},
		MockRawResponseSequence: []string{"42", "processed: 42"},
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "transform",
				Type:           taskengine.ParseNumber,
				PromptTemplate: `Convert to number: {{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "process"},
					},
				},
			},
			{
				ID:             "process",
				Type:           taskengine.RawString,
				InputVar:       "transform", // Use output from previous task
				PromptTemplate: `Process the number: {{.transform}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "forty-two", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "processed: 42", result)
}

func TestUnit_SimpleEnv_ExecEnv_InputVar_WithModeration(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutputSequence:      []any{8, "user message stored"},
		MockRawResponseSequence: []string{"8", "user message stored"},
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "moderate",
				Type:           taskengine.ParseNumber,
				PromptTemplate: `Rate safety of: {{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpGreaterThan, When: "5", Goto: "store"},
						{Operator: "default", Goto: "reject"},
					},
				},
			},
			{
				ID:       "store",
				Type:     taskengine.Hook,
				InputVar: "input", // Use original input despite moderation
				Hook: &taskengine.HookCall{
					Type: "store_message",
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
			{
				ID:             "reject",
				Type:           taskengine.RawString,
				PromptTemplate: `Rejected: {{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "safe message", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "user message stored", result)
}

func TestUnit_SimpleEnv_ExecEnv_InputVar_InvalidVariable(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{} // Shouldn't be called

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				InputVar:       "nonexistent",
				PromptTemplate: `Should fail`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	_, _, err = env.ExecEnv(context.Background(), chain, "test", taskengine.DataTypeString)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input variable")
}

func TestUnit_SimpleEnv_ExecEnv_InputVar_DefaultBehavior(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutputSequence:      []any{"first", "second"},
		MockRawResponseSequence: []string{"first", "second"},
	}

	tracker := activitytracker.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, taskengine.NewAlertSink(&libkv.VKManager{}), mockExec, &taskengine.SimpleInspector{})
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				PromptTemplate: `First: {{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "task2"},
					},
				},
			},
			{
				ID:   "task2",
				Type: taskengine.RawString,
				// No InputVar specified - should use previous output
				PromptTemplate: `Second: {{.task1}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, _, err := env.ExecEnv(context.Background(), chain, "input", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "second", result)
}
