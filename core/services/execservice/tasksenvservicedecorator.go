package execservice

import (
	"context"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/taskengine"
)

type activityTrackerTaskEnvDecorator struct {
	service TasksEnvService
	tracker serverops.ActivityTracker
}

func (d *activityTrackerTaskEnvDecorator) Execute(ctx context.Context, chain *taskengine.ChainDefinition, input string) (any, error) {
	// Extract useful metadata from the chain
	chainID := chain.ID

	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"execute",
		"task-chain",
		"chainID", chainID,
		"inputLength", len(input),
	)
	defer endFn()

	result, err := d.service.Execute(ctx, chain, input)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(chainID, map[string]interface{}{
			"input":   input,
			"result":  result,
			"chainID": chainID,
		})
	}

	return result, err
}

func (d *activityTrackerTaskEnvDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerTaskEnvDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func (d *activityTrackerTaskEnvDecorator) Supports(ctx context.Context) ([]string, error) {
	return d.service.Supports(ctx)
}

func EnvWithActivityTracker(service TasksEnvService, tracker serverops.ActivityTracker) TasksEnvService {
	return &activityTrackerTaskEnvDecorator{
		service: service,
		tracker: tracker,
	}
}
