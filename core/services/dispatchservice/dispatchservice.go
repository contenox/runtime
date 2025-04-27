package dispatchservice

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
)

type JobInfo struct {
	ID           string    `json:"id"`
	TaskType     string    `json:"taskType"`
	ScheduledFor time.Time `json:"scheduledFor"`
	ValidUntil   time.Time `json:"validUntil"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Service interface {
	PendingJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.Job, error)
	InProgressJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.LeasedJob, error)

	AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.LeasedJob, error)
	MarkJobAsDone(ctx context.Context, jobID string, leaserID string) error
	MarkJobAsFailed(ctx context.Context, jobID string, leaserID string) error

	serverops.ServiceMeta
}

type service struct {
	dbInstance libdb.DBManager
}

func New(dbInstance libdb.DBManager, config *serverops.Config) *service {
	return &service{
		dbInstance: dbInstance,
	}
}

// AssignPendingJob implements Service.
func (s *service) AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.LeasedJob, error) {
	if len(jobTypes) == 0 {
		return nil, errors.New("no job types provided")
	}
	tx, com, end, err := s.dbInstance.WithTransaction(ctx)
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	defer end()

	storeInstance := store.New(tx)

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
	job, err := storeInstance.GetLeasedJob(ctx, pendingJob.ID)
	if err != nil {
		return nil, err
	}
	err = com(ctx)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}

func (s *service) GetServiceName() string {
	return "dispatchservice"
}

func (s *service) MarkJobAsDone(ctx context.Context, jobID string, leaserID string) error {
	if err := serverops.CheckServiceAuthorization(ctx, store.New(s.dbInstance.WithoutTransaction()), s, store.PermissionManage); err != nil {
		return err
	}
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	job, err := storeInstance.GetLeasedJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Leaser != leaserID {
		return fmt.Errorf("job %s is not leased by %s", jobID, leaserID)
	}
	err = storeInstance.DeleteLeasedJob(ctx, jobID)
	if err != nil {
		return err
	}
	return nil
}

func (s *service) MarkJobAsFailed(ctx context.Context, jobID string, leaserID string) error {
	tx, com, end, err := s.dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer end()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	storeInstance := store.New(tx)
	job, err := storeInstance.GetLeasedJob(ctx, jobID)
	if err != nil {
		return err
	}
	err = storeInstance.DeleteLeasedJob(ctx, jobID)
	if err != nil {
		return err
	}
	job.RetryCount += 1
	storeInstance.AppendJob(ctx, job.Job)
	err = com(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *service) PendingJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.Job, error) {
	if err := serverops.CheckServiceAuthorization(ctx, store.New(s.dbInstance.WithoutTransaction()), s, store.PermissionView); err != nil {
		return nil, err
	}
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	jobs, err := storeInstance.ListJobs(ctx, createdAtCursor, 1000)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return []*store.Job{}, nil
	}
	return jobs, nil
}

func (s *service) InProgressJobs(ctx context.Context, createdAtCursor *time.Time) ([]*store.LeasedJob, error) {
	if err := serverops.CheckServiceAuthorization(ctx, store.New(s.dbInstance.WithoutTransaction()), s, store.PermissionView); err != nil {
		return nil, err
	}
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	jobs, err := storeInstance.ListLeasedJobs(ctx, createdAtCursor, 1000)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return []*store.LeasedJob{}, nil
	}
	return jobs, nil
}

func NewService(dbInstance libdb.DBManager) Service {
	return &service{
		dbInstance: dbInstance,
	}
}
