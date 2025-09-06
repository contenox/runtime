package playground_test

import (
	"strings"
	"testing"

	"github.com/contenox/runtime/playground"
	"github.com/contenox/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystem_TasksEnvService(t *testing.T) {
	ctx := t.Context()

	p := playground.New()
	p.WithPostgresTestContainer(ctx)
	p.WithNats(ctx)
	p.WithRuntimeState(ctx, true)
	p.WithMockTokenizer()
	p.WithMockHookRegistry()
	p.WithInternalPromptExecutor(ctx, "smollm2:135m", 2048)
	p.WithOllamaBackend(ctx, "prompt-backend", "latest", false, true)
	p.StartBackgroundRoutines(ctx)
	p.WithLLMRepo()

	require.NoError(t, p.GetError(), "Playground setup failed")
	defer p.CleanUp()

	err := p.WaitUntilModelIsReady(ctx, "prompt-backend", "smollm2:135m")
	require.NoError(t, err, "Model not ready in time")

	tasksEnvService, err := p.GetTasksEnvService(ctx)
	require.NoError(t, err, "Failed to get TasksEnvService")

	t.Run("ExecuteSimplePromptChain", func(t *testing.T) {
		chain := &taskengine.TaskChainDefinition{
			ID:          "test-chain",
			Description: "Test chain for simple prompt execution",
			TokenLimit:  2048,
			Tasks: []taskengine.TaskDefinition{
				{
					ID:             "get_answer",
					Description:    "Get answer from LLM",
					Handler:        taskengine.HandleRawString,
					PromptTemplate: "Answer in one word: What is the color of the sky?",
					ExecuteConfig: &taskengine.LLMExecutionConfig{
						Model:    "smollm2:135m",
						Provider: "ollama",
					},
					Transition: taskengine.TaskTransition{
						OnFailure: "",
						Branches: []taskengine.TransitionBranch{
							{
								Operator: taskengine.OpDefault,
								Goto:     taskengine.TermEnd,
							},
						},
					},
				},
			},
		}

		input := "ignored input"
		output, outputType, capturedStates, err := tasksEnvService.Execute(
			ctx,
			chain,
			input,
			taskengine.DataTypeString,
		)

		require.NoError(t, err)
		assert.Equal(t, taskengine.DataTypeString, outputType)
		assert.NotEmpty(t, output)
		assert.Len(t, capturedStates, 1)
		assert.Equal(t, "get_answer", capturedStates[0].TaskID)
	})

	t.Run("ExecuteConditionKeyChain", func(t *testing.T) {
		chain := &taskengine.TaskChainDefinition{
			ID:          "condition-test-chain",
			Description: "Test chain with condition key",
			TokenLimit:  2048,
			Tasks: []taskengine.TaskDefinition{
				{
					ID:             "check_condition",
					Description:    "Check if input is positive",
					Handler:        taskengine.HandleConditionKey,
					PromptTemplate: "Is this a positive statement? Answer only 'yes' or 'no': {{.input}}",
					ValidConditions: map[string]bool{
						"yes":  true,
						"Yes":  true,
						"Yes.": true,
						"y":    true,
						"Y":    true,
						"Y.":   true,
						"no":   false,
						"No":   false,
						"No.":  false,
						"n":    false,
						"N":    false,
						"N.":   false,
					},
					ExecuteConfig: &taskengine.LLMExecutionConfig{
						Model:    "smollm2:135m",
						Provider: "ollama",
					},
					Transition: taskengine.TaskTransition{
						OnFailure: "",
						Branches: []taskengine.TransitionBranch{
							{
								Operator: taskengine.OpDefault,
								Goto:     taskengine.TermEnd,
							},
						},
					},
				},
			},
		}

		input := "I love this beautiful day"
		output, outputType, capturedStates, err := tasksEnvService.Execute(
			ctx,
			chain,
			input,
			taskengine.DataTypeString,
		)

		require.NoError(t, err)
		assert.Equal(t, taskengine.DataTypeBool, outputType)
		assert.NotNil(t, output)
		assert.Len(t, capturedStates, 1)
	})

	t.Run("ExecuteJSONInputPropertyAccess", func(t *testing.T) {
		chain := &taskengine.TaskChainDefinition{
			ID:          "json-input-access-chain",
			Description: "Test chain for accessing JSON input properties",
			TokenLimit:  2048,
			Debug:       true,
			Tasks: []taskengine.TaskDefinition{
				{
					ID:      "check_boiling_point",
					Handler: taskengine.HandleRawString,
					SystemInstruction: "You are a precise science assistant. Water boils at exactly 100°C or 212°F. " +
						"If the temperature is >= boiling point, respond with exactly 'yes'. " +
						"If the temperature is < boiling point, respond with exactly 'no'. " +
						"Do not add any explanation or additional text.",
					PromptTemplate: `Is this temperature where water would boil?
Temperature: {{.input.temperature}}
Unit: {{.input.unit}}`,
					ExecuteConfig: &taskengine.LLMExecutionConfig{
						Model:       "smollm2:135m",
						Provider:    "ollama",
						Temperature: 0.0,
					},
					Transition: taskengine.TaskTransition{
						Branches: []taskengine.TransitionBranch{
							{
								Operator: taskengine.OpDefault,
								Goto:     taskengine.TermEnd,
							},
						},
					},
				},
			},
		}

		// JSON input with temperature and unit
		inputJSON := map[string]interface{}{
			"temperature": 100,
			"unit":        "C",
		}

		// Execute the chain with JSON input
		output, outputType, capturedStates, err := tasksEnvService.Execute(
			ctx,
			chain,
			inputJSON,
			taskengine.DataTypeJSON,
		)

		// Verify execution succeeded
		require.NoError(t, err)
		assert.Equal(t, taskengine.DataTypeString, outputType)
		assert.Len(t, capturedStates, 1)
		assert.Equal(t, "check_boiling_point", capturedStates[0].TaskID)

		// Check that the template rendered correctly
		step := capturedStates[0]
		assert.Contains(t, step.Input, "Temperature: 100", "Template should have rendered temperature correctly")
		assert.Contains(t, step.Input, "Unit: C", "Template should have rendered unit correctly")

		// Verify the output is "yes" (case-insensitive)
		outputStr := strings.ToLower(strings.TrimSpace(output.(string)))
		assert.Contains(t, outputStr, "yes",
			"Should indicate that 100°C is boiling point of water. Actual output: %s", output)
	})
}
