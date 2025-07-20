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
	"github.com/google/uuid"
)

type JobInfo struct {
	ID           string    `json:"id"`
	TaskType     string    `json:"taskType"`
	ScheduledFor time.Time `json:"scheduledFor"`
	ValidUntil   time.Time `json:"validUntil"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Service interface {
	CreateJob(ctx context.Context, job *CreateJobRequest) error
	PendingJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.Job, error)
	InProgressJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.LeasedJob, error)

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

type CreateJobRequest struct {
	TaskType     string    `json:"taskType"`
	Operation    string    `json:"operation"`
	Subject      string    `json:"subject"`
	EntityID     string    `json:"entityId"`
	EntityType   string    `json:"entityType"`
	Payload      []byte    `json:"payload"`
	ScheduledFor time.Time `json:"scheduledFor"`
	ValidUntil   time.Time `json:"validUntil"`
}

func (s *service) CreateJob(ctx context.Context, job *CreateJobRequest) error {
	// Validate job
	if job.TaskType == "" {
		return errors.New("task type is required")
	}
	if job.ScheduledFor.IsZero() {
		return errors.New("scheduledFor is required")
	}
	if job.ValidUntil.IsZero() {
		return errors.New("validUntil is required")
	}
	if job.ValidUntil.Before(job.ScheduledFor) {
		return errors.New("validUntil must be after scheduledFor")
	}
	if job.Operation == "" {
		return errors.New("operation is required")
	}
	if job.Subject == "" {
		return errors.New("subject is required")
	}

	// Convert to store.Job
	storeJob := store.Job{
		ID:           uuid.New().String(),
		TaskType:     job.TaskType,
		Operation:    job.Operation,
		Subject:      job.Subject,
		EntityID:     job.EntityID,
		EntityType:   job.EntityType,
		Payload:      job.Payload,
		ScheduledFor: job.ScheduledFor.Unix(),
		ValidUntil:   job.ValidUntil.Unix(),
		RetryCount:   0,
		CreatedAt:    time.Now().UTC(),
	}

	// Check authorization
	storeInstance := store.New(s.dbInstance.WithoutTransaction())

	return storeInstance.AppendJob(ctx, storeJob)
}

// AssignPendingJob implements Service.
func (s *service) AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.LeasedJob, error) {
	if len(jobTypes) == 0 {
		return nil, errors.New("no job types provided")
	}
	tx, com, end, err := s.dbInstance.WithTransaction(ctx)
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
	storeInstance := store.New(tx)
	job, err := storeInstance.GetLeasedJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Leaser != leaserID {
		return fmt.Errorf("job %s not leased by %s", jobID, leaserID)
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

func (s *service) PendingJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.Job, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	if limit <= 0 {
		return nil, fmt.Errorf("invalid limit %d", limit)
	}
	if limit > 1000 {
		return nil, fmt.Errorf("invalid limit %d", limit)
	}
	jobs, err := storeInstance.ListJobs(ctx, createdAtCursor, limit)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return []*store.Job{}, nil
	}
	return jobs, nil
}

func (s *service) InProgressJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*store.LeasedJob, error) {
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	if limit <= 0 {
		return nil, fmt.Errorf("invalid limit %d", limit)
	}
	if limit > 1000 {
		return nil, fmt.Errorf("invalid limit %d", limit)
	}
	jobs, err := storeInstance.ListLeasedJobs(ctx, createdAtCursor, limit)
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
