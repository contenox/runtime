package modelservice

import (
	"context"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) Append(ctx context.Context, model *store.Model) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"model",
		"name", model.Model,
	)
	defer endFn()

	err := d.service.Append(ctx, model)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(model.Model, map[string]interface{}{
			"name": model.Model,
		})
	}

	return err
}

func (d *activityTrackerDecorator) List(ctx context.Context) ([]*store.Model, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "list", "models")
	defer endFn()

	models, err := d.service.List(ctx)
	if err != nil {
		reportErrFn(err)
	}

	return models, err
}

func (d *activityTrackerDecorator) Delete(ctx context.Context, modelName string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"model",
		"name", modelName,
	)
	defer endFn()

	err := d.service.Delete(ctx, modelName)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(modelName, nil)
	}

	return err
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func WithActivityTracker(service Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}

var _ serverops.ServiceMeta = (*activityTrackerDecorator)(nil)
