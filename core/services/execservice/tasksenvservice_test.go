package execservice_test

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/execservice"
	"github.com/js402/cate/core/services/testingsetup"
	"github.com/js402/cate/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestTasksservice(t *testing.T) {
	if os.Getenv("SMOKETESTS") == "" {
		t.Skip("Set env SMOKETESTS to true to run this test")
	}
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:0.5b",
	}

	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	defer cleanup()
	execRepo, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	exec, err := taskengine.NewExec(ctx, execRepo, nil) // TODO:
	if err != nil {
		log.Fatalf("initializing the taskengine failed: %v", err)
	}
	env, err := taskengine.NewEnv(ctx, serverops.NoopTracker{}, exec)
	if err != nil {
		log.Fatalf("initializing the tasksenv failed: %v", err)
	}
	service := execservice.NewTasksEnv(ctx, env, dbInstance)

	require.Eventually(t, func() bool {
		currentState := state.Get(ctx)
		r, err := json.Marshal(currentState)
		if err != nil {
			t.Logf("error marshaling state: %v", err)
			return false
		}
		dst := &bytes.Buffer{}
		if err := json.Compact(dst, r); err != nil {
			t.Logf("error compacting JSON: %v", err)
			return false
		}
		return strings.Contains(string(r), `"name":"qwen2.5:0.5b"`)
	}, 2*time.Minute, 100*time.Millisecond)
	runtime := state.Get(ctx)
	backendID := ""
	foundExecModel := false
	for _, runtimeState := range runtime {
		backendID = runtimeState.Backend.ID
		for _, lmr := range runtimeState.PulledModels {
			if lmr.Model == "qwen2.5:0.5b" {
				foundExecModel = true
			}
		}
	}
	if !foundExecModel {
		t.Fatalf("qwen2.5:0.5b not found")
	}
	err = store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backendID)
	if err != nil {
		t.Fatalf("failed to assign backend to pool: %v", err)
	}
	// sanity-check
	backends, err := store.New(dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, serverops.EmbedPoolID)
	if err != nil {
		t.Fatalf("failed to list backends for pool: %v", err)
	}
	found2 := false
	for _, backend2 := range backends {
		found2 = backend2.ID == backendID
		if found2 {
			break
		}
	}
	if !found2 {
		t.Fatalf("backend not found in pool")
	}

	t.Run("simple echo task", func(t *testing.T) {
		input := "Hello, world!"
		output, err := service.Execute(ctx, &taskengine.ChainDefinition{
			ID:              "echo-chain",
			Description:     "Echo input string",
			RoutingStrategy: "random",
			MaxTokenSize:    1000,
			Tasks: []taskengine.ChainTask{
				{
					ID:             "echo-task",
					Description:    "Just echo back the input",
					Type:           taskengine.PromptToString,
					PromptTemplate: "{{.input}}",
					Transition: taskengine.Transition{
						OnError: "",
						Next: []taskengine.ConditionalTransition{
							{ID: "end", Value: "_default"},
						},
					},
					PreferredModels: []string{"qwen2.5:0.5b"},
				},
			},
		}, input)

		require.NoError(t, err)
		require.IsType(t, "", output)
		require.Contains(t, strings.ToLower(output.(string)), "hello")
	})
	t.Run("conditional transition test", func(t *testing.T) {
		input := "Should I go outside if it is sunny?"

		chain := &taskengine.ChainDefinition{
			ID:              "test-cond-transition",
			Description:     "A chain that branches on yes/no",
			MaxTokenSize:    1000,
			RoutingStrategy: "random",
			Tasks: []taskengine.ChainTask{
				{
					ID:             "check_weather",
					Description:    "Ask whether it's good to go outside",
					Type:           taskengine.PromptToCondition,
					PromptTemplate: "{{.input}}",
					ConditionMapping: map[string]bool{
						"yes": true,
						"no":  false,
					},
					Transition: taskengine.Transition{
						OnError: "",
						Next: []taskengine.ConditionalTransition{
							{
								Operator: "equals",
								Value:    "true",
								ID:       "do_go",
							},
							{
								Operator: "equals",
								Value:    "_default",
								ID:       "dont_go",
							},
						},
					},
				},
				{
					ID:             "do_go",
					Description:    "Print go message",
					Type:           taskengine.PromptToString,
					PromptTemplate: "Say: Good idea to go outside.",
					Print:          "Decision: {{.do_go}}",
					Transition: taskengine.Transition{
						Next: []taskengine.ConditionalTransition{
							{Value: "_default", ID: "end"},
						},
					},
				},
				{
					ID:             "dont_go",
					Description:    "Print stay message",
					Type:           taskengine.PromptToString,
					PromptTemplate: "Say: Better stay inside.",
					Print:          "Decision: {{.dont_go}}",
					Transition: taskengine.Transition{
						Next: []taskengine.ConditionalTransition{
							{Value: "_default", ID: "end"},
						},
					},
				},
			},
		}

		result, err := service.Execute(ctx, chain, input)
		require.NoError(t, err)
		t.Logf("Final output: %v", result)
	})
	t.Run("number parsing and range branching", func(t *testing.T) {
		input := "How many hours of sleep are recommended for adults? answer strictly in numbers like 3 or ranges like 4-5"

		chain := &taskengine.ChainDefinition{
			ID:              "test-number-branching",
			Description:     "Branch based on number of hours",
			MaxTokenSize:    1000,
			RoutingStrategy: "random",
			Tasks: []taskengine.ChainTask{
				{
					ID:             "ask_sleep_hours",
					Description:    "Ask for number of hours of sleep",
					Type:           taskengine.PromptToRange,
					PromptTemplate: "{{.input}}",
					Transition: taskengine.Transition{
						OnError: "",
						Next: []taskengine.ConditionalTransition{
							{Operator: "lt", Value: "6", ID: "too_little"},
							{Operator: "between", Value: "6-8", ID: "just_right"},
							{Operator: "gt", Value: "9", ID: "too_much"},
						},
					},
				},
				{
					ID:             "too_little",
					Description:    "Comment on little sleep",
					Type:           taskengine.PromptToString,
					PromptTemplate: "Respond that this is too little sleep.",
					Print:          "Result: {{.too_little}}",
					Transition:     taskengine.Transition{Next: []taskengine.ConditionalTransition{{Value: "_default", ID: "end"}}},
				},
				{
					ID:             "just_right",
					Description:    "Comment on adequate sleep",
					Type:           taskengine.PromptToString,
					PromptTemplate: "Respond that this is a good amount of sleep.",
					Print:          "Result: {{.just_right}}",
					Transition:     taskengine.Transition{Next: []taskengine.ConditionalTransition{{Value: "_default", ID: "end"}}},
				},
				{
					ID:             "too_much",
					Description:    "Comment on too much sleep",
					Type:           taskengine.PromptToString,
					PromptTemplate: "Respond that this might be too much sleep.",
					Print:          "Result: {{.too_much}}",
					Transition:     taskengine.Transition{Next: []taskengine.ConditionalTransition{{Value: "_default", ID: "end"}}},
				},
			},
		}

		result, err := service.Execute(ctx, chain, input)
		require.NoError(t, err)
		t.Logf("Final output: %v", result)
	})
}
