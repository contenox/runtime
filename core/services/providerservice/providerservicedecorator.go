package providerservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) SetProviderConfig(ctx context.Context, providerType string, config *serverops.ProviderConfig) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"set",
		"provider_config",
		"provider_type", providerType,
	)
	defer endFn()

	err := d.service.SetProviderConfig(ctx, providerType, config)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(providerType, config)
	}

	return err
}

func (d *activityTrackerDecorator) GetProviderConfig(ctx context.Context, providerType string) (*serverops.ProviderConfig, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"get",
		"provider_config",
		"provider_type", providerType,
	)
	defer endFn()

	config, err := d.service.GetProviderConfig(ctx, providerType)
	if err != nil {
		reportErrFn(err)
	}
	return config, err
}

func (d *activityTrackerDecorator) DeleteProviderConfig(ctx context.Context, providerType string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"provider_config",
		"provider_type", providerType,
	)
	defer endFn()

	err := d.service.DeleteProviderConfig(ctx, providerType)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(providerType, nil)
	}

	return err
}

func (d *activityTrackerDecorator) ListProviderConfigs(ctx context.Context) ([]*serverops.ProviderConfig, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"list",
		"provider_configs",
	)
	defer endFn()

	configs, err := d.service.ListProviderConfigs(ctx)
	if err != nil {
		reportErrFn(err)
	}
	return configs, err
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
