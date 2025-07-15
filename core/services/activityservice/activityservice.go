package activityservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/activity"
	"github.com/contenox/runtime-mvp/core/serverops"
)

type Service interface {
	GetLogs(ctx context.Context, limit int) ([]activity.TrackedEvent, error)
	GetRequests(ctx context.Context, limit int) ([]activity.TrackedRequest, error)
	GetRequest(ctx context.Context, reqID string) ([]activity.TrackedEvent, error)
	serverops.ServiceMeta
}

type service struct {
	tracker *activity.KVActivityTracker
}

// GetServiceGroup implements Service.
func (s *service) GetServiceGroup() string {
	return "activityservice"
}

// GetServiceName implements Service.
func (s *service) GetServiceName() string {
	return "activityservice"
}

func New(tracker *activity.KVActivityTracker) Service {
	return &service{tracker: tracker}
}

func (s *service) GetLogs(ctx context.Context, limit int) ([]activity.TrackedEvent, error) {
	return s.tracker.GetActivityLogs(ctx, limit)
}

func (s *service) GetRequests(ctx context.Context, limit int) ([]activity.TrackedRequest, error) {
	return s.tracker.GetRecentRequestIDs(ctx, limit)
}

func (s *service) GetRequest(ctx context.Context, reqID string) ([]activity.TrackedEvent, error) {
	return s.tracker.GetActivityLogsByRequestID(ctx, reqID)
}
