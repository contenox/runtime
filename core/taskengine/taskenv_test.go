package taskengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestSimpleEnv_ExecEnv_SingleTask(t *testing.T) {
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "42",
		MockRawResponse: "42",
		MockError:       nil,
	}

	tracker := serverops.NoopTracker{}
	env, err := taskengine.NewEnv(context.Background(), tracker, mockExec)
	require.NoError(t, err)

	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:             "task1",
				Type:           taskengine.PromptToString,
				PromptTemplate: `What is {{.input}}?`,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{
							Operator: "equals",
							Value:    "42",
							ID:       "end",
						},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "6 * 7")
	require.NoError(t, err)
	require.Equal(t, "42", result)
}

func TestSimpleEnv_ExecEnv_FailsAfterRetries(t *testing.T) {
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
				Type:           taskengine.PromptToString,
				PromptTemplate: `Broken task`,
				RetryOnError:   1,
				Transition:     taskengine.Transition{},
			},
		},
	}

	_, err = env.ExecEnv(context.Background(), chain, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed after 1 retries")
}

func TestSimpleEnv_ExecEnv_TransitionsToNextTask(t *testing.T) {
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
				ID:             "task1",
				Type:           taskengine.PromptToString,
				PromptTemplate: `{{.input}}`,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Operator: "equals", Value: "continue", ID: "task2"},
					},
				},
			},
			{
				ID:             "task2",
				Type:           taskengine.PromptToString,
				PromptTemplate: `Follow up`,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Operator: "equals", Value: "continue", ID: "end"},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "step one")
	require.NoError(t, err)
	require.Equal(t, "intermediate", result)
}

func TestSimpleEnv_ExecEnv_ErrorTransition(t *testing.T) {
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
				ID:             "task1",
				Type:           taskengine.PromptToString,
				PromptTemplate: `fail`,
				Transition: taskengine.Transition{
					OnError: "task2",
				},
			},
			{
				ID:             "task2",
				Type:           taskengine.PromptToString,
				PromptTemplate: `recover`,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Operator: "equals", Value: "recovered", ID: "end"},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "oops")
	require.NoError(t, err)
	require.Equal(t, "error recovered", result)
}

func TestSimpleEnv_ExecEnv_PrintTemplate(t *testing.T) {
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
				ID:             "task1",
				Type:           taskengine.PromptToString,
				PromptTemplate: `hi {{.input}}`,
				Print:          `Output: {{.task1}}`,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Operator: "equals", Value: "printed-value", ID: "end"},
					},
				},
			},
		},
	}

	result, err := env.ExecEnv(context.Background(), chain, "user")
	require.NoError(t, err)
	require.Equal(t, "printed-value", result)
}
