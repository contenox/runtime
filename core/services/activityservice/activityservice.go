package activityservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/activity"
)

type Service interface {
	GetLogs(ctx context.Context, limit int) ([]activity.TrackedEvent, error)
}

type service struct {
	tracker *activity.KVActivityTracker
}

func New(tracker *activity.KVActivityTracker) Service {
	return &service{tracker: tracker}
}

func (s *service) GetLogs(ctx context.Context, limit int) ([]activity.TrackedEvent, error) {
	return s.tracker.GetActivityLogs(ctx, limit)
}
