package downloadservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) CurrentDownloadQueueState(ctx context.Context) ([]Job, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "list", "download-queue")
	defer endFn()

	jobs, err := d.service.CurrentDownloadQueueState(ctx)
	if err != nil {
		reportErrFn(err)
	}

	return jobs, err
}

func (d *activityTrackerDecorator) CancelDownloads(ctx context.Context, url string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"cancel",
		"download",
		"url", url,
	)
	defer endFn()

	err := d.service.CancelDownloads(ctx, url)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(url, map[string]interface{}{
			"status": "cancelled",
		})
	}

	return err
}

func (d *activityTrackerDecorator) RemoveDownloadFromQueue(ctx context.Context, modelName string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"remove",
		"download-job",
		"model", modelName,
	)
	defer endFn()

	err := d.service.RemoveDownloadFromQueue(ctx, modelName)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(modelName, map[string]interface{}{
			"status": "removed",
		})
	}

	return err
}

func (d *activityTrackerDecorator) DownloadInProgress(ctx context.Context, statusCh chan<- *store.Status) error {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "stream", "download-status")
	defer endFn()

	err := d.service.DownloadInProgress(ctx, statusCh)
	if err != nil {
		reportErrFn(err)
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

var _ Service = (*activityTrackerDecorator)(nil)
