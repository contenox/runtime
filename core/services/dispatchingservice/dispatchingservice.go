package dispatchingservice

import (
	"context"
	"time"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
)

type JobInfo struct {
	ID           string    `json:"id"`
	TaskType     string    `json:"taskType"`
	ScheduledFor time.Time `json:"scheduledFor"`
	ValidUntil   time.Time `json:"validUntil"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Service interface {
	PendingJobs(ctx context.Context) ([]JobInfo, error)
	AssignPendingJob(ctx context.Context, leaserID string, leaseDuration *time.Duration, jobTypes ...string) (*store.Job, error)
	MarkJobAsDone(ctx context.Context, jobID string, leaserID string) error
	MarkJobAsFailed(ctx context.Context, jobID string, leaserID string) error

	serverops.ServiceMeta
}
