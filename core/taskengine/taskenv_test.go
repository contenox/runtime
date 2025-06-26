package taskengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_SimpleEnv_ExecEnv_SingleTask(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "42",
		MockRawResponse: "42",
		MockError:       nil,
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(t.Context(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:       "task1",
				Type:     taskengine.RawString,
				Template: `What is {{.input}}?`,
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

	result, err := env.ExecEnv(context.Background(), chain, "6 * 7", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "42", result)
}

func TestUnit_SimpleEnv_ExecEnv_FailsAfterRetries(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockError: errors.New("permanent failure"),
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.RawString,
				Template:       `Broken task`,
				RetryOnFailure: 1,
				Transition:     taskengine.TaskTransition{},
			},
		},
	}

	_, err = env.ExecEnv(context.Background(), chain, "", taskengine.DataTypeString)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed after 1 retries")
}

func TestUnit_SimpleEnv_ExecEnv_TransitionsToNextTask(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "intermediate",
		MockRawResponse: "continue",
		MockError:       nil,
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:       "task1",
				Type:     taskengine.RawString,
				Template: `{{.input}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "continue", Goto: "task2"},
					},
				},
			},
			{
				ID:       "task2",
				Type:     taskengine.RawString,
				Template: `Follow up`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "continue", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "step one", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "intermediate", result)
}

func TestUnit_SimpleEnv_ExecEnv_ErrorTransition(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		ErrorSequence:   []error{errors.New("first failure"), nil},
		MockOutput:      "error recovered",
		MockRawResponse: "recovered",
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:       "task1",
				Type:     taskengine.RawString,
				Template: `fail`,
				Transition: taskengine.TaskTransition{
					OnFailure: "task2",
				},
			},
			{
				ID:       "task2",
				Type:     taskengine.RawString,
				Template: `recover`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "recovered", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "oops", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "error recovered", result)
}

func TestUnit_SimpleEnv_ExecEnv_PrintTemplate(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "printed-value",
		MockRawResponse: "printed-value",
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:       "task1",
				Type:     taskengine.RawString,
				Template: `hi {{.input}}`,
				Print:    `Output: {{.task1}}`,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "equals", When: "printed-value", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "user", taskengine.DataTypeString)
	require.NoError(t, err)
	require.Equal(t, "printed-value", result)
}
