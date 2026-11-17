package contenoxcli

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/taskengine"
)

func completionChain() (*taskengine.TaskChainDefinition, error) {
	var chain taskengine.TaskChainDefinition
	if err := json.Unmarshal([]byte(initFIMChain), &chain); err != nil {
		return nil, err
	}
	return &chain, nil
}

// inferenceSetupFactory also serves a fresh install, before an agent engine has a default model.
func inferenceSetupFactory(db libdbexec.DBManager, bus libbus.Messenger, active *enginesvc.Engine, state *runtimestate.State, tracker libtracker.ActivityTracker, workspaceID string) func(context.Context) (*inferencesetup.Service, *runtimestate.State, func(), error) {
	var stateMu sync.Mutex
	discovery := func(ctx context.Context) (*runtimestate.State, error) {
		stateMu.Lock()
		defer stateMu.Unlock()
		if state != nil {
			return state, nil
		}
		var err error
		state, err = runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels(), runtimestate.WithTracker(tracker))
		return state, err
	}
	return func(ctx context.Context) (*inferencesetup.Service, *runtimestate.State, func(), error) {
		chain, err := completionChain()
		if err != nil {
			return nil, nil, nil, err
		}
		engine := active
		closeSetup := func() {}
		if engine == nil {
			state, err := discovery(ctx)
			if err != nil {
				return nil, nil, nil, err
			}
			engine, err = enginesvc.Build(ctx, db, enginesvc.Config{
				Bus: bus, State: state, Tracker: tracker, WorkspaceID: workspaceID, SkipBackendCycle: true,
			})
			if err != nil {
				return nil, nil, nil, err
			}
			closeSetup = engine.Stop
		}
		factory, err := inferencesetup.NewOperationModelFactory(inferencesetup.OperationDeps{
			DB: db, Bus: engine.Bus, State: engine.State, Tracker: tracker, Models: engine.Models,
			Agent: agentservice.New(agentservice.Deps{Engine: engine, DB: db, WorkspaceID: workspaceID}), Chain: chain,
		})
		if err != nil {
			closeSetup()
			return nil, nil, nil, err
		}
		service, err := inferencesetup.New(db, engine.State, engine.Bus, factory)
		if err != nil {
			closeSetup()
			return nil, nil, nil, err
		}
		return service, engine.State, closeSetup, nil
	}
}
