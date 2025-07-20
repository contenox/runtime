package dispatchservice

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

// AuthorizationDecorator wraps a Service and adds authorization checks
type AuthorizationDecorator struct {
	service    Service
	dbInstance libdb.DBManager
}

func WithAuthorization(service Service, dbInstance libdb.DBManager) Service {
	return &AuthorizationDecorator{
		service:    service,
		dbInstance: dbInstance,
	}
}

func (d *AuthorizationDecorator) CreateJob(ctx context.Context, job *CreateJobRequest) error {
	storeInstance := store.New(d.dbInstance.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionManage); err != nil {
		return err
	}
	return d.service.CreateJob(ctx, job)
}

func (d *AuthorizationDecorator) PendingJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.Job, error) {
	storeInstance := store.New(d.dbInstance.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionView); err != nil {
		return nil, err
	}
	return d.service.PendingJobs(ctx, createdAtCursor, limit)
}

func (d *AuthorizationDecorator) InProgressJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.LeasedJob, error) {
	storeInstance := store.New(d.dbInstance.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionView); err != nil {
		return nil, err
	}
	return d.service.InProgressJobs(ctx, createdAtCursor, limit)
}

func (d *AuthorizationDecorator) AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.LeasedJob, error) {
	tx, com, end, err := d.dbInstance.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer end()

	storeInstance := store.New(tx)
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionManage); err != nil {
		return nil, err
	}

	// Create a temporary service for the transaction
	txService := &txService{
		service:    d.service,
		dbInstance: d.dbInstance,
		tx:         tx,
	}

	job, err := txService.AssignPendingJob(ctx, leaserID, leaseDuration, jobTypes...)
	if err != nil {
		return nil, err
	}

	if err := com(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func (d *AuthorizationDecorator) MarkJobAsDone(ctx context.Context, jobID string, leaserID string) error {
	storeInstance := store.New(d.dbInstance.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionManage); err != nil {
		return err
	}
	return d.service.MarkJobAsDone(ctx, jobID, leaserID)
}

func (d *AuthorizationDecorator) MarkJobAsFailed(ctx context.Context, jobID string, leaserID string) error {
	storeInstance := store.New(d.dbInstance.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, d, store.PermissionManage); err != nil {
		return err
	}
	return d.service.MarkJobAsFailed(ctx, jobID, leaserID)
}

func (d *AuthorizationDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func (d *AuthorizationDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

// txService handles transactional operations
type txService struct {
	service    Service
	dbInstance libdb.DBManager
	tx         libdb.Exec
}

func (s *txService) AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.LeasedJob, error) {
	if len(jobTypes) == 0 {
		return nil, errors.New("no job types provided")
	}

	storeInstance := store.New(s.tx)
	index := rand.Intn(len(jobTypes))
	pendingJob, err := storeInstance.PopJobForType(ctx, jobTypes[index])
	if err != nil {
		return nil, err
	}

	duration := time.Duration(10)
	if leaseDuration != nil {
		duration = *leaseDuration
	}

	err = storeInstance.AppendLeasedJob(ctx, *pendingJob, duration, leaserID)
	if err != nil {
		return nil, err
	}

	return storeInstance.GetLeasedJob(ctx, pendingJob.ID)
}

// Other methods are not needed for txService since we only use it for AssignPendingJob
func (s *txService) CreateJob(ctx context.Context, job *CreateJobRequest) error {
	return fmt.Errorf("not implemented")
}

func (s *txService) PendingJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.Job, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *txService) InProgressJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.LeasedJob, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *txService) MarkJobAsDone(ctx context.Context, jobID string, leaserID string) error {
	return fmt.Errorf("not implemented")
}

func (s *txService) MarkJobAsFailed(ctx context.Context, jobID string, leaserID string) error {
	return fmt.Errorf("not implemented")
}

func (s *txService) GetServiceGroup() string {
	return s.service.GetServiceGroup()
}

func (s *txService) GetServiceName() string {
	return s.service.GetServiceName()
}
