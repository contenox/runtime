package execservice_test

import (
	"context"
	"log"
	"strings"
	"testing"

	"github.com/contenox/contenox/core/hooks"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/services/execservice"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestSystem_ExecService_FullTaskExecutionPipeline(t *testing.T) {
	config := &serverops.Config{
		JWTExpiry:  "1h",
		TasksModel: "qwen2.5:0.5b",
	}

	testenv := testingsetup.New(context.Background(), serverops.NoopTracker{}).
		WithTriggerChan().
		WithServiceManager(config).
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		RunState().
		RunDownloadManager().
		Build()
	defer testenv.Cleanup()
	ctx := testenv.Ctx
	require.NoError(t, testenv.Err)
	execRepo, err := testenv.NewExecRepo(config)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	exec, err := taskengine.NewExec(ctx, execRepo, hooks.NewMockHookRegistry())
	if err != nil {
		log.Fatalf("initializing the taskengine failed: %v", err)
	}
	env, err := taskengine.NewEnv(ctx, serverops.NoopTracker{}, exec)
	if err != nil {
		log.Fatalf("initializing the tasksenv failed: %v", err)
	}
	service := execservice.NewTasksEnv(ctx, env, testenv.GetDBInstance(), hooks.NewMockHookRegistry())
	require.NoError(t, testenv.WaitForModel(config.TasksModel).Err)
	require.NoError(t, testenv.AssignBackends(serverops.EmbedPoolID).Err)
	t.Run("simple echo task", func(t *testing.T) {
		output, err := service.Execute(ctx, &taskengine.ChainDefinition{
			ID:              "echo-chain",
			Description:     "Echo input string",
			RoutingStrategy: "random",
			MaxTokenSize:    1000,
			Tasks: []taskengine.ChainTask{
				{
					ID:          "echo-task",
					Description: "Just echo back the input",
					Type:        taskengine.PromptToString,
					Template:    "echo back the input without any explanation. Input: {{.input}}",
					Transition: taskengine.Transition{
						OnError: "",
						Next: []taskengine.ConditionalTransition{
							{ID: "end", Value: "default"},
						},
					},
					PreferredModels: []string{config.TasksModel},
				},
			},
		}, "Hello, world!")

		require.NoError(t, err)
		require.IsType(t, "", output)
		require.Equal(t, strings.ToLower(output.(string)), "hello, world!")
	})
}
