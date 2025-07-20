package dispatchservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/dispatchservice"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func setupFileServiceTestEnv(ctx context.Context, t *testing.T) (libdb.DBManager, dispatchservice.Service, func()) {
	t.Helper()
	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(ctx, uuid.NewString(), "test", "test")
	if err != nil {
		t.Fatalf("failed to setup local database: %v", err)
	}

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	if err != nil {
		t.Fatalf("failed to create new Postgres DB Manager: %v", err)
	}
	err = serverops.NewServiceManager(&serverops.Config{
		JWTExpiry:       "1h",
		SecurityEnabled: "false",
	})
	if err != nil {
		t.Fatalf("failed to create new Service Manager: %v", err)
	}
	dispatchService := dispatchservice.New(dbInstance, &serverops.Config{
		JWTExpiry:       "1h",
		SecurityEnabled: "false",
	})

	return dbInstance, dispatchService, dbCleanup
}

func TestUnit_DispatchService_AssignsPendingJobSuccessfully(t *testing.T) {
	ctx := context.Background()
	db, service, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()

	// Setup test data
	storeSvc := store.New(db.WithoutTransaction())
	jobType := "test-assign-job"
	testJob := &store.Job{
		ID:           uuid.NewString(),
		TaskType:     jobType,
		Payload:      []byte("{}"),
		ScheduledFor: time.Now().Unix(),
		ValidUntil:   time.Now().Add(1 * time.Hour).Unix(),
	}
	require.NoError(t, storeSvc.AppendJob(ctx, *testJob))

	t.Run("successful_assignment", func(t *testing.T) {
		leasedJob, err := service.AssignPendingJob(ctx, "leaser-1", nil, jobType)
		require.NoError(t, err)
		require.Equal(t, testJob.ID, leasedJob.ID)

		// Verify job moved to leased
		_, err = storeSvc.GetLeasedJob(ctx, testJob.ID)
		require.NoError(t, err)

		// Verify removed from queue
		jobs, err := storeSvc.PopJobsForType(ctx, jobType)
		require.NoError(t, err)
		require.Empty(t, jobs)
	})

	t.Run("no_available_jobs", func(t *testing.T) {
		_, err := service.AssignPendingJob(ctx, "leaser-1", nil, "non-existent-type")
		require.Error(t, err)
	})
}

func TestUnit_DispatchService_MarksJobAsDoneRemovesLease(t *testing.T) {
	ctx := context.Background()
	db, service, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()

	storeInstance := store.New(db.WithoutTransaction())
	job := createTestJob(t, storeInstance, "test-done-job")

	// Assign job first
	_, err := service.AssignPendingJob(ctx, "leaser-1", nil, job.TaskType)
	require.NoError(t, err)

	t.Run("successful_completion", func(t *testing.T) {
		err := service.MarkJobAsDone(ctx, job.ID, "leaser-1")
		require.NoError(t, err)

		// Verify job removed from leased
		_, err = storeInstance.GetLeasedJob(ctx, job.ID)
		require.Error(t, err)
	})

	t.Run("invalid_job_id", func(t *testing.T) {
		err := service.MarkJobAsDone(ctx, "invalid-id", "leaser-1")
		require.Error(t, err)
	})
}

func TestUnit_DispatchService_MarksJobAsFailedRequeuesWithRetry(t *testing.T) {
	ctx := context.Background()
	db, service, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()

	storeInstance := store.New(db.WithoutTransaction())
	job := createTestJob(t, storeInstance, "test-failed-job")
	originalRetries := job.RetryCount

	// Assign job first
	_, err := service.AssignPendingJob(ctx, "leaser-1", nil, job.TaskType)
	require.NoError(t, err)

	t.Run("successful_retry", func(t *testing.T) {
		err := service.MarkJobAsFailed(ctx, job.ID, "leaser-1")
		require.NoError(t, err)

		// Verify job removed from leased
		_, err = storeInstance.GetLeasedJob(ctx, job.ID)
		require.Error(t, err)

		// Verify job requeued with incremented retry
		popped, err := storeInstance.PopJobForType(ctx, job.TaskType)
		require.NoError(t, err)
		require.Equal(t, originalRetries+1, popped.RetryCount)
	})

	t.Run("invalid_leaser_id", func(t *testing.T) {
		err := service.MarkJobAsFailed(ctx, job.ID, "wrong-leaser")
		require.Error(t, err)
	})

	t.Run("job_already_deleted", func(t *testing.T) {
		job := createTestJob(t, storeInstance, "test-deleted-job")
		originalRetries := job.RetryCount

		// Assign job
		_, err := service.AssignPendingJob(ctx, "leaser-1", nil, job.TaskType)
		require.NoError(t, err)

		// Manually delete leased job (simulate external cleanup/expiry)
		err = storeInstance.DeleteLeasedJob(ctx, job.ID)
		require.NoError(t, err)

		// Attempt to mark as failed
		err = service.MarkJobAsFailed(ctx, job.ID, "leaser-1")
		require.Error(t, err, "should error when job missing")
		require.Contains(t, err.Error(), "not found", "error should indicate missing job")

		// Verify not requeued
		requeued, err := storeInstance.PopJobForType(ctx, job.TaskType)
		require.Error(t, err, "should have no jobs to pop")
		require.Nil(t, requeued, "no job should exist in queue")

		// Verify retry count unchanged
		if requeued != nil {
			require.Equal(t, originalRetries, requeued.RetryCount,
				"retry count should not increment")
		}
	})
}

func TestUnit_DispatchService_ListsPendingAndInProgressJobs(t *testing.T) {
	ctx := context.Background()
	db, service, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()
	storeInstance := store.New(db.WithoutTransaction())
	jobType := "test-listing-job"

	// Create pending job
	pendingJob := createTestJob(t, storeInstance, jobType)

	// Create leased job
	leasedJob := &store.Job{
		ID:           uuid.NewString(),
		TaskType:     "test-listing-job",
		Payload:      []byte("{}"),
		ScheduledFor: time.Now().Unix(),
		ValidUntil:   time.Now().Add(1 * time.Hour).Unix(),
		CreatedAt:    time.Now().UTC(),
	}
	require.NoError(t, storeInstance.AppendLeasedJob(ctx, *leasedJob, 30*time.Minute, "leaser-1"))

	t.Run("pending_jobs", func(t *testing.T) {
		jobs, err := service.PendingJobs(ctx, nil)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, pendingJob.ID, jobs[0].ID)
	})

	t.Run("in_progress_jobs", func(t *testing.T) {
		jobs, err := service.InProgressJobs(ctx, nil)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, leasedJob.ID, jobs[0].ID)
	})
}

func createTestJob(t *testing.T, storeInstance store.Store, taskType string) *store.Job {
	job := &store.Job{
		ID:           uuid.NewString(),
		TaskType:     taskType,
		Payload:      []byte("{}"),
		ScheduledFor: time.Now().Unix(),
		ValidUntil:   time.Now().Add(1 * time.Hour).Unix(),
	}
	require.NoError(t, storeInstance.AppendJob(context.Background(), *job))
	return job
}
