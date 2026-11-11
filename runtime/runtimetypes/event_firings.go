package runtimetypes

import (
	"context"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libevents"
)

var EventConsumerConfig = libevents.Config{TablePrefix: "event_", ScopeColumn: "workspace_id"}

var eventStoreConfig = EventConsumerConfig

const (
	EventFiringStatusRunning = libevents.FiringStatusRunning
	EventFiringStatusOK      = libevents.FiringStatusOK
	EventFiringStatusError   = libevents.FiringStatusError
	EventFiringStatusRefused = libevents.FiringStatusRefused
)

const (
	DefaultEventFiringLimit = libevents.DefaultFiringLimit
	MaxEventFiringLimit     = libevents.MaxFiringLimit
)

const StaleEventFiringClaim = 2 * time.Hour

type EventFiring struct {
	WorkspaceID string
	TriggerName string
	NID         int64
	Status      string
	Error       string
	RequestID   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (f EventFiring) Stranded(now time.Time) bool {
	return libevents.Firing{Status: f.Status, UpdatedAt: f.UpdatedAt}.Stranded(now, StaleEventFiringClaim)
}

type EventFiringFilter struct {
	SinceNID int64

	Status string

	TriggerName string

	Limit int
}

type EventFiringStore interface {
	GetEventCursor(ctx context.Context, consumer string) (int64, error)

	SetEventCursor(ctx context.Context, consumer string, nid int64) error

	BeginEventFiring(ctx context.Context, triggerName string, nid int64, requestID string) (bool, error)

	FinishEventFiring(ctx context.Context, triggerName string, nid int64, status, errMsg string) error

	ListEventFirings(ctx context.Context, f EventFiringFilter) ([]EventFiring, error)
}

type eventFiringStore struct {
	exec    libdb.Exec
	cursors *libevents.CursorStore
	firings *libevents.FiringStore
}

type EventFiringStoreOption func(*eventFiringStoreOptions)

type eventFiringStoreOptions struct {
	now func() time.Time
}

func WithEventFiringClock(now func() time.Time) EventFiringStoreOption {
	return func(o *eventFiringStoreOptions) {
		if now != nil {
			o.now = now
		}
	}
}

func NewEventFiringStore(exec libdb.Exec, workspaceID string, opts ...EventFiringStoreOption) (EventFiringStore, error) {
	if exec == nil {
		return nil, fmt.Errorf("runtimetypes: event firing store requires an exec")
	}
	o := eventFiringStoreOptions{now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	cursors, err := libevents.NewCursorStore(eventStoreConfig, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("runtimetypes: event cursor store: %w", err)
	}
	firings, err := libevents.NewFiringStore(eventStoreConfig, workspaceID, StaleEventFiringClaim, libevents.WithClock(o.now))
	if err != nil {
		return nil, fmt.Errorf("runtimetypes: event firing store: %w", err)
	}
	return &eventFiringStore{exec: exec, cursors: cursors, firings: firings}, nil
}

func (s *eventFiringStore) GetEventCursor(ctx context.Context, consumer string) (int64, error) {
	return s.cursors.GetCursor(ctx, s.exec, consumer)
}

func (s *eventFiringStore) SetEventCursor(ctx context.Context, consumer string, nid int64) error {
	return s.cursors.SetCursor(ctx, s.exec, consumer, nid)
}

func (s *eventFiringStore) BeginEventFiring(ctx context.Context, triggerName string, nid int64, requestID string) (bool, error) {
	return s.firings.BeginFiring(ctx, s.exec, triggerName, nid, requestID)
}

func (s *eventFiringStore) FinishEventFiring(ctx context.Context, triggerName string, nid int64, status, errMsg string) error {
	return s.firings.FinishFiring(ctx, s.exec, triggerName, nid, status, errMsg)
}

func (s *eventFiringStore) ListEventFirings(ctx context.Context, f EventFiringFilter) ([]EventFiring, error) {
	firings, err := s.firings.ListFirings(ctx, s.exec, libevents.FiringFilter{
		SinceNID:    f.SinceNID,
		Status:      f.Status,
		TriggerName: f.TriggerName,
		Limit:       f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]EventFiring, 0, len(firings))
	for _, fr := range firings {
		out = append(out, EventFiring{
			WorkspaceID: fr.Scope,
			TriggerName: fr.TriggerName,
			NID:         fr.NID,
			Status:      fr.Status,
			Error:       fr.Error,
			RequestID:   fr.RequestID,
			CreatedAt:   fr.CreatedAt,
			UpdatedAt:   fr.UpdatedAt,
		})
	}
	return out, nil
}
