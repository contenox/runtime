package telegramservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) Create(ctx context.Context, frontend *store.TelegramFrontend) error {
	reportErr, reportChange, end := d.tracker.Start(
		ctx,
		"create",
		"telegram_frontend",
		"user_id", frontend.UserID,
	)
	defer end()

	err := d.service.Create(ctx, frontend)
	if err != nil {
		reportErr(err)
	} else {
		// Mask sensitive data before reporting
		masked := *frontend
		masked.BotToken = "********"
		reportChange(frontend.ID, &masked)
	}
	return err
}

func (d *activityTrackerDecorator) Update(ctx context.Context, frontend *store.TelegramFrontend) error {
	reportErr, reportChange, end := d.tracker.Start(
		ctx,
		"update",
		"telegram_frontend",
		"id", frontend.ID,
		"user_id", frontend.UserID,
	)
	defer end()

	err := d.service.Update(ctx, frontend)
	if err != nil {
		reportErr(err)
	} else {
		// Mask sensitive data before reporting
		masked := *frontend
		masked.BotToken = "********"
		reportChange(frontend.ID, &masked)
	}
	return err
}

func (d *activityTrackerDecorator) Get(ctx context.Context, id string) (*store.TelegramFrontend, error) {
	reportErr, _, end := d.tracker.Start(
		ctx,
		"get",
		"telegram_frontend",
		"id", id,
	)
	defer end()

	frontend, err := d.service.Get(ctx, id)
	if err != nil {
		reportErr(err)
	}
	return frontend, err
}

func (d *activityTrackerDecorator) Delete(ctx context.Context, id string) error {
	reportErr, reportChange, end := d.tracker.Start(
		ctx,
		"delete",
		"telegram_frontend",
		"id", id,
	)
	defer end()

	err := d.service.Delete(ctx, id)
	if err != nil {
		reportErr(err)
	} else {
		reportChange(id, nil)
	}
	return err
}

func (d *activityTrackerDecorator) List(ctx context.Context) ([]*store.TelegramFrontend, error) {
	// reportErr, _, end := d.tracker.Start(
	// 	ctx,
	// 	"list",
	// 	"telegram_frontends",
	// )
	// defer end()

	frontends, err := d.service.List(ctx)
	// if err != nil {
	// 	reportErr(err)
	// }
	return frontends, err
}

func (d *activityTrackerDecorator) ListByUser(ctx context.Context, userID string) ([]*store.TelegramFrontend, error) {
	reportErr, _, end := d.tracker.Start(
		ctx,
		"list",
		"telegram_frontends",
		"user_id", userID,
	)
	defer end()

	frontends, err := d.service.ListByUser(ctx, userID)
	if err != nil {
		reportErr(err)
	}
	return frontends, err
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func WithServiceActivityTracker(service Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}

var _ serverops.ServiceMeta = (*activityTrackerDecorator)(nil)
