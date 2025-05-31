package accessservice

import (
	"context"
	"time"

	"github.com/contenox/contenox/core/serverops"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) Create(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"access_entry",
		"identity", entry.Identity,
		"resource", entry.Resource,
		"resourceType", entry.ResourceType,
		"permission", entry.Permission,
	)
	defer endFn()

	result, err := d.service.Create(ctx, entry)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(result.ID, map[string]interface{}{
			"identity":     result.Identity,
			"resource":     result.Resource,
			"resourceType": result.ResourceType,
			"permission":   result.Permission,
		})
	}

	return result, err
}

func (d *activityTrackerDecorator) GetByID(ctx context.Context, entry AccessEntryRequest) (*AccessEntryRequest, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"access_entry",
		"entryID", entry.ID,
		"withDetails", entry.WithUserDetails != nil && *entry.WithUserDetails,
	)
	defer endFn()

	result, err := d.service.GetByID(ctx, entry)
	if err != nil {
		reportErrFn(err)
	}

	return result, err
}

func (d *activityTrackerDecorator) Update(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"update",
		"access_entry",
		"entryID", entry.ID,
		"identity", entry.Identity,
		"resource", entry.Resource,
		"resourceType", entry.ResourceType,
		"permission", entry.Permission,
	)
	defer endFn()

	result, err := d.service.Update(ctx, entry)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(result.ID, map[string]interface{}{
			"identity":     result.Identity,
			"resource":     result.Resource,
			"resourceType": result.ResourceType,
			"permission":   result.Permission,
		})
	}

	return result, err
}

func (d *activityTrackerDecorator) Delete(ctx context.Context, id string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"access_entry",
		"entryID", id,
	)
	defer endFn()

	err := d.service.Delete(ctx, id)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(id, nil)
	}

	return err
}

func (d *activityTrackerDecorator) ListAll(ctx context.Context, starting time.Time, withDetails bool) ([]AccessEntryRequest, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"list",
		"access_entries",
		"starting", starting,
		"withDetails", withDetails,
	)
	defer endFn()

	result, err := d.service.ListAll(ctx, starting, withDetails)
	if err != nil {
		reportErrFn(err)
	}

	return result, err
}

func (d *activityTrackerDecorator) ListByIdentity(ctx context.Context, identity string, withDetails bool) ([]AccessEntryRequest, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"list",
		"access_entries",
		"identity", identity,
		"withDetails", withDetails,
	)
	defer endFn()

	result, err := d.service.ListByIdentity(ctx, identity, withDetails)
	if err != nil {
		reportErrFn(err)
	}

	return result, err
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

// WithActivityTracker decorates the given Service with activity tracking
func WithActivityTracker(service Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}

var _ Service = (*activityTrackerDecorator)(nil)
