package eventsourceservice

import (
	"context"
	"time"

	"github.com/contenox/runtime/eventstore"
	"github.com/contenox/runtime/libtracker"
)

// activityTrackerDecorator implements Service with activity tracking
type activityTrackerDecorator struct {
	service Service
	tracker libtracker.ActivityTracker
}

// AppendEvent implements Service interface with activity tracking
func (d *activityTrackerDecorator) AppendEvent(ctx context.Context, event *eventstore.Event) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"append",
		"event",
		"event_type", event.EventType,
		"aggregate_type", event.AggregateType,
		"aggregate_id", event.AggregateID,
	)
	defer endFn()

	err := d.service.AppendEvent(ctx, event)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(event.ID, map[string]interface{}{
			"event_type":     event.EventType,
			"aggregate_type": event.AggregateType,
			"aggregate_id":   event.AggregateID,
			"version":        event.Version,
			"created_at":     event.CreatedAt.Format(time.RFC3339Nano),
		})
	}

	return err
}

// GetEventsByAggregate implements Service interface with activity tracking
func (d *activityTrackerDecorator) GetEventsByAggregate(ctx context.Context, eventType string, from, to time.Time, aggregateType, aggregateID string, limit int) ([]eventstore.Event, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"query",
		"events_by_aggregate",
		"event_type", eventType,
		"aggregate_type", aggregateType,
		"aggregate_id", aggregateID,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"limit", limit,
	)
	defer endFn()

	events, err := d.service.GetEventsByAggregate(ctx, eventType, from, to, aggregateType, aggregateID, limit)
	if err != nil {
		reportErrFn(err)
	}
	return events, err
}

// GetEventsByType implements Service interface with activity tracking
func (d *activityTrackerDecorator) GetEventsByType(ctx context.Context, eventType string, from, to time.Time, limit int) ([]eventstore.Event, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"query",
		"events_by_type",
		"event_type", eventType,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"limit", limit,
	)
	defer endFn()

	events, err := d.service.GetEventsByType(ctx, eventType, from, to, limit)
	if err != nil {
		reportErrFn(err)
	}
	return events, err
}

func (d *activityTrackerDecorator) GetEventsBySource(ctx context.Context, eventType string, from, to time.Time, eventSource string, limit int) ([]eventstore.Event, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"query",
		"events_by_source",
		"event_type", eventType,
		"event_source", eventSource,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"limit", limit,
	)
	defer endFn()

	events, err := d.service.GetEventsBySource(ctx, eventType, from, to, eventSource, limit)
	if err != nil {
		reportErrFn(err)
	}
	return events, err
}

// GetEventTypesInRange implements Service interface with activity tracking
func (d *activityTrackerDecorator) GetEventTypesInRange(ctx context.Context, from, to time.Time, limit int) ([]string, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"query",
		"event_types_in_range",
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"limit", limit,
	)
	defer endFn()

	eventTypes, err := d.service.GetEventTypesInRange(ctx, from, to, limit)
	if err != nil {
		reportErrFn(err)
	}
	return eventTypes, err
}

// DeleteEventsByTypeInRange implements Service interface with activity tracking
func (d *activityTrackerDecorator) DeleteEventsByTypeInRange(ctx context.Context, eventType string, from, to time.Time) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"events_by_type",
		"event_type", eventType,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
	)
	defer endFn()

	err := d.service.DeleteEventsByTypeInRange(ctx, eventType, from, to)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(eventType, map[string]interface{}{
			"from":      from.Format(time.RFC3339),
			"to":        to.Format(time.RFC3339),
			"operation": "delete_events_by_type",
		})
	}

	return err
}

// SubscribeToEvents implements Service.
func (d *activityTrackerDecorator) SubscribeToEvents(ctx context.Context, eventType string, ch chan<- []byte) (Subscription, error) {
	return d.service.SubscribeToEvents(ctx, eventType, ch)
}

// WithActivityTracker decorates a Service with activity tracking
func WithActivityTracker(service Service, tracker libtracker.ActivityTracker) Service {
	if service == nil {
		panic("service cannot be nil")
	}
	if tracker == nil {
		panic("tracker cannot be nil")
	}
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}
