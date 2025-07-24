package taskengine_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_ComposeOverride(t *testing.T) {
	// Setup environment
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      map[string]any{"b": 3, "c": 4},
		MockRawResponse: "",
	}
	env := setupTestEnv(mockExec)

	// Define chain with compose task
	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:   "task1",
				Type: taskengine.RawString,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: "task2"},
					},
				},
			},
			{
				ID:   "task2",
				Type: taskengine.RawString,
				Compose: &taskengine.ComposeTask{
					WithVar:  "task1",
					Strategy: "override",
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	// Execute chain
	output, _, err := env.ExecEnv(context.Background(), chain, map[string]any{"a": 1, "b": 2}, taskengine.DataTypeJSON)
	require.NoError(t, err)

	// Verify composition
	expected := map[string]any{"a": 1, "b": 2, "c": 4}
	assert.Equal(t, expected, output)
}

func TestUnit_ComposeAppendStringToChatHistory(t *testing.T) {
	// Setup environment
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput:      "System message",
		MockRawResponse: "",
	}
	env := setupTestEnv(mockExec)

	// Define chain
	rightChat := taskengine.ChatHistory{
		Messages: []taskengine.Message{
			{Role: "user", Content: "Hello"},
		},
	}
	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:   "task1",
				Type: taskengine.RawString,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: "task2"},
					},
				},
			},
			{
				ID:   "task2",
				Type: taskengine.RawString,
				Compose: &taskengine.ComposeTask{
					WithVar:  "task1",
					Strategy: "append_string_to_chat_history",
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	// Execute chain
	output, _, err := env.ExecEnv(context.Background(), chain, rightChat, taskengine.DataTypeChatHistory)
	require.NoError(t, err)

	// Verify composition
	composed, ok := output.(taskengine.ChatHistory)
	require.True(t, ok)
	expected := taskengine.ChatHistory{
		Messages: []taskengine.Message{
			{Role: "system", Content: "System message"},
			{Role: "user", Content: "Hello"},
		},
	}
	assert.Equal(t, expected.Messages, composed.Messages)
}

func TestUnit_ComposeMergeChatHistories(t *testing.T) {
	// Setup environment
	mockExec := &taskengine.MockTaskExecutor{
		MockOutput: taskengine.ChatHistory{
			Messages: []taskengine.Message{
				{Role: "user", Content: "Hello"},
			},
			Model:        "gpt-3.5",
			InputTokens:  10,
			OutputTokens: 20,
		},
		MockRawResponse: "",
	}
	env := setupTestEnv(mockExec)

	// Define chain
	rightChat := taskengine.ChatHistory{
		Messages: []taskengine.Message{
			{Role: "assistant", Content: "Hi!"},
		},
		Model:        "gpt-4",
		InputTokens:  5,
		OutputTokens: 15,
	}
	chain := &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:   "task1",
				Type: taskengine.RawString,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: "task2"},
					},
				},
			},
			{
				ID:   "task2",
				Type: taskengine.RawString,
				Compose: &taskengine.ComposeTask{
					WithVar:  "task1",
					Strategy: "merge_chat_histories",
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: taskengine.OpDefault, Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}

	// Execute chain
	output, _, err := env.ExecEnv(context.Background(), chain, rightChat, taskengine.DataTypeChatHistory)
	require.NoError(t, err)

	// Verify composition
	composed, ok := output.(taskengine.ChatHistory)
	require.True(t, ok)
	expected := taskengine.ChatHistory{
		Messages: []taskengine.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!"},
		},
		InputTokens:  15,
		OutputTokens: 35,
		Model:        "", // Models differ, so cleared
	}
	assert.Equal(t, expected, composed)
}

func TestUnit_ComposeAutoStrategy(t *testing.T) {
	t.Run("AutoSelectsMergeForChatHistories", func(t *testing.T) {
		// Setup environment
		mockExec := &taskengine.MockTaskExecutor{
			MockOutput: taskengine.ChatHistory{
				Messages: []taskengine.Message{{Role: "user", Content: "Hi"}},
			},
		}
		env := setupTestEnv(mockExec)

		// Define chain with empty strategy
		chain := &taskengine.ChainDefinition{
			Tasks: []taskengine.ChainTask{
				{
					ID:   "task1",
					Type: taskengine.RawString,
					Transition: taskengine.TaskTransition{
						Branches: []taskengine.TransitionBranch{
							{Operator: taskengine.OpDefault, Goto: "task2"},
						},
					},
				},
				{
					ID:   "task2",
					Type: taskengine.RawString,
					Compose: &taskengine.ComposeTask{
						WithVar: "task1", // Strategy omitted for auto-selection
					},
				},
			},
		}

		// Execute with chat history input
		_, _, err := env.ExecEnv(
			context.Background(),
			chain,
			taskengine.ChatHistory{},
			taskengine.DataTypeChatHistory,
		)

		// Should succeed with merge strategy
		assert.NoError(t, err)
	})

	t.Run("AutoSelectsOverrideForOtherTypes", func(t *testing.T) {
		// Setup environment
		mockExec := &taskengine.MockTaskExecutor{
			MockOutput: "test output",
		}
		env := setupTestEnv(mockExec)

		// Define chain with empty strategy
		chain := &taskengine.ChainDefinition{
			Tasks: []taskengine.ChainTask{
				{
					ID:   "task1",
					Type: taskengine.RawString,
					Transition: taskengine.TaskTransition{
						Branches: []taskengine.TransitionBranch{
							{Operator: taskengine.OpDefault, Goto: "task2"},
						},
					},
				},
				{
					ID:   "task2",
					Type: taskengine.RawString,
					Compose: &taskengine.ComposeTask{
						WithVar: "task1", // Strategy omitted
					},
				},
			},
		}

		// Execute with string input
		_, _, err := env.ExecEnv(
			context.Background(),
			chain,
			"right value",
			taskengine.DataTypeString,
		)

		// Should succeed with override strategy
		assert.NoError(t, err)
	})
}

func TestUnit_ComposeErrors(t *testing.T) {
	t.Run("UnsupportedStrategy", func(t *testing.T) {
		// Setup environment
		mockExec := &taskengine.MockTaskExecutor{MockOutput: "test"}
		env := setupTestEnv(mockExec)

		// Define chain with invalid strategy
		chain := &taskengine.ChainDefinition{
			Tasks: []taskengine.ChainTask{
				{
					ID:   "task1",
					Type: taskengine.RawString,
					Transition: taskengine.TaskTransition{
						Branches: []taskengine.TransitionBranch{
							{Operator: taskengine.OpDefault, Goto: "task2"},
						},
					},
				},
				{
					ID:   "task2",
					Type: taskengine.RawString,
					Compose: &taskengine.ComposeTask{
						WithVar:  "task1",
						Strategy: "invalid_strategy",
					},
				},
			},
		}

		// Execute chain
		_, _, err := env.ExecEnv(context.Background(), chain, "input", taskengine.DataTypeString)

		// Verify error
		assert.ErrorContains(t, err, "unsupported compose strategy")
	})

	t.Run("MissingRightVar", func(t *testing.T) {
		// Setup environment
		mockExec := &taskengine.MockTaskExecutor{MockOutput: "test"}
		env := setupTestEnv(mockExec)

		// Define chain with missing right variable
		chain := &taskengine.ChainDefinition{
			Tasks: []taskengine.ChainTask{
				{
					ID:   "task1",
					Type: taskengine.RawString,
					Compose: &taskengine.ComposeTask{
						WithVar: "nonexistent",
					},
				},
			},
		}

		// Execute chain
		_, _, err := env.ExecEnv(context.Background(), chain, "input", taskengine.DataTypeString)

		// Verify error
		assert.ErrorContains(t, err, "compose right_var \"nonexistent\" not found")
	})
}

// Helper to create test environment
func setupTestEnv(exec taskengine.TaskExecutor) taskengine.EnvExecutor {
	// Create no-op dependencies
	tracker := &serverops.NoopTracker{}
	alerts := &taskengine.NoopAlertSink{}
	inspector := &taskengine.NoopInspector{}

	env, _ := taskengine.NewEnv(
		context.Background(),
		tracker,
		alerts,
		exec,
		inspector,
	)
	return env
}
