package execservice_test

import (
	"bytes"
	"encoding/json"
	"log"
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
	// if os.Getenv("SMOKETESTS") == "" {
	// 	t.Skip("Set env SMOKETESTS to true to run this test")
	// }
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:3b",
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
		return strings.Contains(string(r), `"name":"qwen2.5:3b"`)
	}, 2*time.Minute, 100*time.Millisecond)
	runtime := state.Get(ctx)
	backendID := ""
	foundExecModel := false
	for _, runtimeState := range runtime {
		backendID = runtimeState.Backend.ID
		for _, lmr := range runtimeState.PulledModels {
			if lmr.Model == "qwen2.5:3b" {
				foundExecModel = true
			}
		}
	}
	if !foundExecModel {
		t.Fatalf("qwen2.5:3b not found")
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
					PromptTemplate: "Just echo back the input: {{.input}}",
					Transition: taskengine.Transition{
						OnError: "",
						Next: []taskengine.ConditionalTransition{
							{ID: "end", Value: "_default"},
						},
					},
					PreferredModels: []string{"qwen2.5:3b"},
				},
			},
		}, "Hello, world!")

		require.NoError(t, err)
		require.IsType(t, "", output)
		require.Equal(t, output.(string), "Hello, world!")
	})
}
