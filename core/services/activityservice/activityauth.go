package activityservice

import (
	"context"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime/taskengine"
)

// authorizationDecorator adds authorization checks to the activity service
type authorizationDecorator struct {
	service Service
	db      libdb.DBManager
}

// authorize checks if the current context has permission for the requested operation
func (a *authorizationDecorator) authorize(ctx context.Context, permission store.Permission) error {
	tx := a.db.WithoutTransaction()
	return serverops.CheckServiceAuthorization(ctx, store.New(tx), a, permission)
}

// View permission methods
func (a *authorizationDecorator) GetLogs(ctx context.Context, limit int) ([]taskengine.TrackedEvent, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetLogs(ctx, limit)
}

func (a *authorizationDecorator) GetRequests(ctx context.Context, limit int) ([]taskengine.TrackedRequest, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetRequests(ctx, limit)
}

func (a *authorizationDecorator) GetRequest(ctx context.Context, reqID string) ([]taskengine.TrackedEvent, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetRequest(ctx, reqID)
}

func (a *authorizationDecorator) GetActivityLogsByRequestID(ctx context.Context, requestID string) ([]taskengine.TrackedEvent, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetActivityLogsByRequestID(ctx, requestID)
}

func (a *authorizationDecorator) GetKnownOperations(ctx context.Context) ([]taskengine.Operation, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetKnownOperations(ctx)
}

func (a *authorizationDecorator) GetRequestIDByOperation(ctx context.Context, operation taskengine.Operation) ([]taskengine.TrackedRequest, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetRequestIDByOperation(ctx, operation)
}

func (a *authorizationDecorator) GetExecutionState(ctx context.Context, reqID string) ([]taskengine.CapturedStateUnit, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetExecutionState(ctx, reqID)
}

func (a *authorizationDecorator) GetStatefulRequests(ctx context.Context) ([]string, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetStatefulRequests(ctx)
}

func (a *authorizationDecorator) FetchAlerts(ctx context.Context, limit int) ([]*taskengine.Alert, error) {
	if err := a.authorize(ctx, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.FetchAlerts(ctx, limit)
}

// ServiceMeta implementation - required for authorization checks
func (a *authorizationDecorator) GetServiceName() string {
	return a.service.GetServiceName()
}

func (a *authorizationDecorator) GetServiceGroup() string {
	return a.service.GetServiceGroup()
}

// WithAuthorization decorates the given Service with authorization checks
func WithAuthorization(service Service, db libdb.DBManager) Service {
	return &authorizationDecorator{
		service: service,
		db:      db,
	}
}

var _ Service = (*authorizationDecorator)(nil)
