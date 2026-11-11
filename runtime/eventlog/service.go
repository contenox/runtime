package eventlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type Service interface {
	Append(ctx context.Context, event *runtimetypes.Event) error
	AppendBatch(ctx context.Context, events []*runtimetypes.Event) error
	ListEventsSince(ctx context.Context, workspaceID string, afterNID int64, limit int) ([]runtimetypes.Event, error)
	ListRecentEvents(ctx context.Context, workspaceID string, beforeNID int64, limit int) ([]runtimetypes.Event, error)
	GetEventsByType(ctx context.Context, workspaceID, eventType string, from, to time.Time, limit int) ([]runtimetypes.Event, error)
	GetEventsBySource(ctx context.Context, workspaceID, eventType string, from, to time.Time, eventSource string, limit int) ([]runtimetypes.Event, error)
	GetEventsBySubject(ctx context.Context, workspaceID, eventType string, from, to time.Time, subject string, limit int) ([]runtimetypes.Event, error)
	GetEventTypesInRange(ctx context.Context, workspaceID string, from, to time.Time, limit int) ([]string, error)
	DeleteEventsByTypeInRange(ctx context.Context, workspaceID, eventType string, from, to time.Time) error

	PrunePartitionsBefore(ctx context.Context, t time.Time) ([]string, error)

	Subscribe(ctx context.Context, eventType string, ch chan<- []byte) (libbus.Subscription, error)
}

type service struct {
	store      runtimetypes.EventStore
	bus        libbus.Messenger
	tracker    libtracker.ActivityTracker
	trigger    Trigger
	partitions partitionCache
}

type partitionCache struct {
	mu      sync.Mutex
	ensured map[string]bool
}

func (c *partitionCache) has(period string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensured[period]
}

func (c *partitionCache) mark(period string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensured[period] = true
}

func (c *partitionCache) forget(periods []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, period := range periods {
		delete(c.ensured, period)
	}
}

type ServiceOption func(*service)

func WithTrigger(t Trigger) ServiceOption {
	return func(s *service) {
		if t != nil {
			s.trigger = t
		}
	}
}

func NewService(db libdb.DBManager, bus libbus.Messenger, tracker libtracker.ActivityTracker, opts ...ServiceOption) Service {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	s := &service{
		store:      runtimetypes.NewEventStore(db),
		bus:        bus,
		tracker:    tracker,
		partitions: partitionCache{ensured: map[string]bool{}},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *service) Append(ctx context.Context, event *runtimetypes.Event) error {
	if event == nil {
		return fmt.Errorf("%w: event cannot be nil", runtimetypes.ErrInvalidEventParameter)
	}
	if err := s.ensurePartition(ctx, event.Time); err != nil {
		return err
	}
	if err := s.store.AppendEvent(ctx, event); err != nil {
		return err
	}

	defer fireTrigger(ctx, s.trigger, event)
	s.publish(ctx, event)
	return nil
}

func (s *service) AppendBatch(ctx context.Context, events []*runtimetypes.Event) error {
	if len(events) == 0 {
		return nil
	}
	for _, event := range events {
		if event == nil {
			return fmt.Errorf("%w: event cannot be nil", runtimetypes.ErrInvalidEventParameter)
		}
		if err := s.ensurePartition(ctx, event.Time); err != nil {
			return err
		}
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return err
	}
	defer fireTrigger(ctx, s.trigger, events...)
	latest := map[string]*runtimetypes.Event{}
	order := []string{}
	for _, event := range events {
		if _, seen := latest[event.Type]; !seen {
			order = append(order, event.Type)
		}
		latest[event.Type] = event
	}
	for _, eventType := range order {
		s.publish(ctx, latest[eventType])
	}
	return nil
}

func (s *service) publish(ctx context.Context, event *runtimetypes.Event) {
	if s.bus == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err == nil {
		err = s.bus.Publish(ctx, event.Type, payload)
	}
	if err != nil {
		reportErr, _, end := s.tracker.Start(ctx, "publish", "event", "type", event.Type, "nid", event.NID)
		reportErr(fmt.Errorf("eventlog: publish after append failed; event %d is durable, live fan-out skipped: %w", event.NID, err))
		end()
	}
}

func (s *service) ensurePartition(ctx context.Context, t time.Time) error {
	now := time.Now().UTC()
	if t.IsZero() {
		t = now
	}
	if t.Before(now.Add(-runtimetypes.EventAcceptanceWindow)) || t.After(now.Add(runtimetypes.EventAcceptanceWindow)) {
		return nil
	}
	period := runtimetypes.EventPeriodKey(t)
	if s.partitions.has(period) {
		return nil
	}
	if err := s.store.EnsureEventPartitionExists(ctx, t); err != nil {
		return err
	}
	s.partitions.mark(period)
	return nil
}

func (s *service) PrunePartitionsBefore(ctx context.Context, t time.Time) ([]string, error) {
	dropped, err := s.store.PruneEventPartitionsBefore(ctx, t)

	s.partitions.forget(dropped)
	return dropped, err
}

func (s *service) ListEventsSince(ctx context.Context, workspaceID string, afterNID int64, limit int) ([]runtimetypes.Event, error) {
	return s.store.ListEventsSince(ctx, workspaceID, afterNID, limit)
}

func (s *service) ListRecentEvents(ctx context.Context, workspaceID string, beforeNID int64, limit int) ([]runtimetypes.Event, error) {
	return s.store.ListRecentEvents(ctx, workspaceID, beforeNID, limit)
}

func (s *service) GetEventsByType(ctx context.Context, workspaceID, eventType string, from, to time.Time, limit int) ([]runtimetypes.Event, error) {
	if err := validateQuery(workspaceID, from, to, limit); err != nil {
		return nil, err
	}
	return s.store.GetEventsByType(ctx, workspaceID, eventType, from, to, limit)
}

func (s *service) GetEventsBySource(ctx context.Context, workspaceID, eventType string, from, to time.Time, eventSource string, limit int) ([]runtimetypes.Event, error) {
	if err := validateQuery(workspaceID, from, to, limit); err != nil {
		return nil, err
	}
	return s.store.GetEventsBySource(ctx, workspaceID, eventType, from, to, eventSource, limit)
}

func (s *service) GetEventsBySubject(ctx context.Context, workspaceID, eventType string, from, to time.Time, subject string, limit int) ([]runtimetypes.Event, error) {
	if err := validateQuery(workspaceID, from, to, limit); err != nil {
		return nil, err
	}
	return s.store.GetEventsBySubject(ctx, workspaceID, eventType, from, to, subject, limit)
}

func (s *service) GetEventTypesInRange(ctx context.Context, workspaceID string, from, to time.Time, limit int) ([]string, error) {
	if err := validateQuery(workspaceID, from, to, limit); err != nil {
		return nil, err
	}
	return s.store.GetEventTypesInRange(ctx, workspaceID, from, to, limit)
}

func (s *service) DeleteEventsByTypeInRange(ctx context.Context, workspaceID, eventType string, from, to time.Time) error {
	if err := requireWorkspace(workspaceID); err != nil {
		return err
	}
	if from.After(to) {
		return fmt.Errorf("%w: from after to", runtimetypes.ErrInvalidEventParameter)
	}
	return s.store.DeleteEventsByTypeInRange(ctx, workspaceID, eventType, from, to)
}

func (s *service) Subscribe(ctx context.Context, eventType string, ch chan<- []byte) (libbus.Subscription, error) {
	if s.bus == nil {
		return nil, fmt.Errorf("eventlog: no bus wired for subscriptions")
	}
	return s.bus.Stream(ctx, eventType, ch)
}

func validateQuery(workspaceID string, from, to time.Time, limit int) error {
	if err := requireWorkspace(workspaceID); err != nil {
		return err
	}
	if from.After(to) {
		return fmt.Errorf("%w: 'from' (%v) after 'to' (%v)", runtimetypes.ErrInvalidEventParameter, from, to)
	}
	if limit <= 0 {
		return fmt.Errorf("%w: limit must be positive", runtimetypes.ErrInvalidEventParameter)
	}
	if limit > runtimetypes.MaxEventListLimit {
		return fmt.Errorf("%w: limit cannot exceed %d", runtimetypes.ErrInvalidEventParameter, runtimetypes.MaxEventListLimit)
	}
	return nil
}

func requireWorkspace(workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("%w: workspace_id is required", runtimetypes.ErrEventMissingRequiredField)
	}
	return nil
}
