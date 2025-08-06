package activityservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime/taskengine"
)

type Service interface {
	GetLogs(ctx context.Context, limit int) ([]taskengine.TrackedEvent, error)
	GetRequests(ctx context.Context, limit int) ([]taskengine.TrackedRequest, error)
	GetRequest(ctx context.Context, reqID string) ([]taskengine.TrackedEvent, error)
	GetKnownOperations(ctx context.Context) ([]taskengine.Operation, error)
	GetRequestIDByOperation(ctx context.Context, operation taskengine.Operation) ([]taskengine.TrackedRequest, error)
	GetActivityLogsByRequestID(ctx context.Context, requestID string) ([]taskengine.TrackedEvent, error)
	GetExecutionState(ctx context.Context, reqID string) ([]taskengine.CapturedStateUnit, error)
	GetStatefulRequests(ctx context.Context) ([]string, error)
	FetchAlerts(ctx context.Context, limit int) ([]*taskengine.Alert, error)

	serverops.ServiceMeta
}

type service struct {
	tracker *taskengine.KVActivitySink
	alerts  taskengine.AlertSink
}

// GetServiceGroup implements Service.
func (s *service) GetServiceGroup() string {
	return "activityservice"
}

// GetServiceName implements Service.
func (s *service) GetServiceName() string {
	return "activityservice"
}

func New(tracker *taskengine.KVActivitySink, alerts taskengine.AlertSink) Service {
	return &service{tracker: tracker, alerts: alerts}
}

func (s *service) GetLogs(ctx context.Context, limit int) ([]taskengine.TrackedEvent, error) {
	return s.tracker.GetActivityLogs(ctx, limit)
}

func (s *service) GetRequests(ctx context.Context, limit int) ([]taskengine.TrackedRequest, error) {
	return s.tracker.GetRecentRequestIDs(ctx, limit)
}

func (s *service) GetRequest(ctx context.Context, reqID string) ([]taskengine.TrackedEvent, error) {
	return s.tracker.GetActivityLogsByRequestID(ctx, reqID)
}

func (s *service) GetActivityLogsByRequestID(ctx context.Context, requestID string) ([]taskengine.TrackedEvent, error) {
	return s.tracker.GetActivityLogsByRequestID(ctx, requestID)
}

// GetKnownOperations implements Service.
func (s *service) GetKnownOperations(ctx context.Context) ([]taskengine.Operation, error) {
	return s.tracker.GetKnownOperations(ctx)
}

// GetRequestIDByOperation implements Service.
func (s *service) GetRequestIDByOperation(ctx context.Context, operation taskengine.Operation) ([]taskengine.TrackedRequest, error) {
	return s.tracker.GetRequestIDByOperation(ctx, operation)
}

func (s *service) GetExecutionState(ctx context.Context, reqID string) ([]taskengine.CapturedStateUnit, error) {
	return s.tracker.GetExecutionStateByRequestID(ctx, reqID)
}

func (s *service) GetStatefulRequests(ctx context.Context) ([]string, error) {
	return s.tracker.GetStatefulRequests(ctx)
}

func (s *service) FetchAlerts(ctx context.Context, limit int) ([]*taskengine.Alert, error) {
	return s.alerts.FetchAlerts(ctx, limit)
}
