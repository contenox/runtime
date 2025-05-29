package userservice

import (
	"context"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) Login(ctx context.Context, email, password string) (*Result, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"login",
		"user",
		"email", email,
	)
	defer endFn()

	result, err := d.service.Login(ctx, email, password)
	if err != nil {
		reportErrFn(err)
	} else if result != nil {
		reportChangeFn(result.User.Subject, map[string]interface{}{
			"email":     result.User.Email,
			"expiresAt": result.ExpiresAt,
		})
	}

	return result, err
}

func (d *activityTrackerDecorator) Register(ctx context.Context, req CreateUserRequest) (*Result, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"register",
		"user",
		"email", req.Email,
	)
	defer endFn()

	result, err := d.service.Register(ctx, req)
	if err != nil {
		reportErrFn(err)
	} else if result != nil {
		reportChangeFn(result.User.Subject, map[string]interface{}{
			"email":        result.User.Email,
			"friendlyName": result.User.FriendlyName,
			"expiresAt":    result.ExpiresAt,
		})
	}

	return result, err
}

func (d *activityTrackerDecorator) CreateUser(ctx context.Context, req CreateUserRequest) (*store.User, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"user",
		"email", req.Email,
		"friendlyName", req.FriendlyName,
	)
	defer endFn()

	user, err := d.service.CreateUser(ctx, req)
	if err != nil {
		reportErrFn(err)
	} else if user != nil {
		reportChangeFn(user.ID, map[string]interface{}{
			"email":        user.Email,
			"friendlyName": user.FriendlyName,
		})
	}

	return user, err
}

func (d *activityTrackerDecorator) DeleteUser(ctx context.Context, id string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"user",
		"userID", id,
	)
	defer endFn()

	err := d.service.DeleteUser(ctx, id)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(id, nil)
	}

	return err
}

func (d *activityTrackerDecorator) GetUserFromContext(ctx context.Context) (*store.User, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "read", "user")
	defer endFn()

	user, err := d.service.GetUserFromContext(ctx)
	if err != nil {
		reportErrFn(err)
	}

	return user, err
}

func (d *activityTrackerDecorator) UpdateUserFields(ctx context.Context, id string, req UpdateUserRequest) (*store.User, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"update",
		"user",
		"userID", id,
	)
	defer endFn()

	user, err := d.service.UpdateUserFields(ctx, id, req)
	if err != nil {
		reportErrFn(err)
	} else if user != nil {
		reportChangeFn(user.ID, map[string]interface{}{
			"email":        user.Email,
			"friendlyName": user.FriendlyName,
		})
	}

	return user, err
}

func (d *activityTrackerDecorator) ListUsers(ctx context.Context, cursorCreatedAt time.Time) ([]*store.User, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "list", "users")
	defer endFn()

	users, err := d.service.ListUsers(ctx, cursorCreatedAt)
	if err != nil {
		reportErrFn(err)
	}

	return users, err
}

func (d *activityTrackerDecorator) GetUserByID(ctx context.Context, id string) (*store.User, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"user",
		"userID", id,
	)
	defer endFn()

	user, err := d.service.GetUserByID(ctx, id)
	if err != nil {
		reportErrFn(err)
	}

	return user, err
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

var _ Service = (*activityTrackerDecorator)(nil)
