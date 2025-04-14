package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/CATE/serverops/store"
	"github.com/stretchr/testify/require"
)

func TestAppendJobAndPopAll(t *testing.T) {
	ctx, s := store.SetupStore(t)

	job := &store.Job{
		ID:           uuid.New().String(),
		TaskType:     "test-task",
		Payload:      []byte(`{"key": "value"}`),
		ScheduledFor: 1620000000,
		ValidUntil:   1620003600,
	}

	// Append the job.
	err := s.AppendJob(ctx, *job)
	require.NoError(t, err)

	// Pop all jobs.
	jobs, err := s.PopAllJobs(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	retrieved := jobs[0]
	require.Equal(t, job.TaskType, retrieved.TaskType)
	require.Equal(t, job.Payload, retrieved.Payload)
	require.Equal(t, job.ScheduledFor, retrieved.ScheduledFor)
	require.Equal(t, job.ValidUntil, retrieved.ValidUntil)
}

func TestPopAllForType(t *testing.T) {
	ctx, s := store.SetupStore(t)

	job1 := &store.Job{
		ID:           uuid.New().String(),
		TaskType:     "type-A",
		Payload:      []byte(`{"foo": "bar"}`),
		ScheduledFor: 1610000000,
		ValidUntil:   1610003600,
	}
	job2 := &store.Job{
		ID:           uuid.New().String(),
		TaskType:     "type-B",
		Payload:      []byte(`{"hello": "world"}`),
		ScheduledFor: 1620000000,
		ValidUntil:   1620003600,
	}

	require.NoError(t, s.AppendJob(ctx, *job1))
	require.NoError(t, s.AppendJob(ctx, *job2))

	// Pop jobs of type-A.
	jobs, err := s.PopJobsForType(ctx, "type-A")
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	retrieved := jobs[0]
	require.Equal(t, job1.TaskType, retrieved.TaskType)
	require.Equal(t, job1.Payload, retrieved.Payload)
	require.Equal(t, job1.ScheduledFor, retrieved.ScheduledFor)
	require.Equal(t, job1.ValidUntil, retrieved.ValidUntil)

	// Ensure type-B job is still in the queue.
	remainingJobs, err := s.PopAllJobs(ctx)
	require.NoError(t, err)
	require.Len(t, remainingJobs, 1)
	require.Equal(t, job2.TaskType, remainingJobs[0].TaskType)
}

func TestPopAllEmptyQueue(t *testing.T) {
	ctx, s := store.SetupStore(t)

	jobs, err := s.PopAllJobs(ctx)
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestPopAllForTypeEmpty(t *testing.T) {
	ctx, s := store.SetupStore(t)

	jobs, err := s.PopJobsForType(ctx, "non-existent-type")
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestPopOneForType(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Prepare valid JSON payloads.
	job1Payload, _ := json.Marshal(map[string]string{"data": "job1"})
	job2Payload, _ := json.Marshal(map[string]string{"data": "job2"})
	job3Payload, _ := json.Marshal(map[string]string{"data": "job3"})

	// Insert three jobs: two of type "task-A", one of type "task-B".
	job1 := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-A",
		Payload:      job1Payload,
		ScheduledFor: 1600000000,
		ValidUntil:   1600003600,
	}
	job2 := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-A",
		Payload:      job2Payload,
		ScheduledFor: 1600000001,
		ValidUntil:   1600003601,
	}
	job3 := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-B",
		Payload:      job3Payload,
		ScheduledFor: 1600000002,
		ValidUntil:   1600003602,
	}

	require.NoError(t, s.AppendJob(ctx, job1))
	time.Sleep(10 * time.Millisecond) // Ensure ordering by created_at.
	require.NoError(t, s.AppendJob(ctx, job2))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, s.AppendJob(ctx, job3))

	// Pop one job of type "task-A" (oldest should be returned).
	poppedJob, err := s.PopJobForType(ctx, "task-A")
	require.NoError(t, err)
	require.NotNil(t, poppedJob)
	require.Equal(t, "task-A", poppedJob.TaskType)
	require.Equal(t, job1.ID, poppedJob.ID)
	require.Equal(t, job1.ScheduledFor, poppedJob.ScheduledFor)
	require.Equal(t, job1.ValidUntil, poppedJob.ValidUntil)

	// Pop another job of type "task-A".
	poppedJob2, err := s.PopJobForType(ctx, "task-A")
	require.NoError(t, err)
	require.NotNil(t, poppedJob2)
	require.Equal(t, "task-A", poppedJob2.TaskType)
	require.Equal(t, job2.ID, poppedJob2.ID)
	require.Equal(t, job2.ScheduledFor, poppedJob2.ScheduledFor)
	require.Equal(t, job2.ValidUntil, poppedJob2.ValidUntil)

	// Try popping another "task-A" job (should return an error or no rows).
	poppedJob3, err := s.PopJobForType(ctx, "task-A")
	require.Error(t, err)
	require.Nil(t, poppedJob3)

	// Ensure "task-B" job is still available.
	poppedJobB, err := s.PopJobForType(ctx, "task-B")
	require.NoError(t, err)
	require.NotNil(t, poppedJobB)
	require.Equal(t, "task-B", poppedJobB.TaskType)
	require.Equal(t, job3.ID, poppedJobB.ID)
	require.Equal(t, job3.ScheduledFor, poppedJobB.ScheduledFor)
	require.Equal(t, job3.ValidUntil, poppedJobB.ValidUntil)
}

func TestGetAllForType(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Prepare valid JSON payloads.
	payloadA1, err := json.Marshal(map[string]string{"job": "A1"})
	require.NoError(t, err)
	payloadA2, err := json.Marshal(map[string]string{"job": "A2"})
	require.NoError(t, err)
	payloadB, err := json.Marshal(map[string]string{"job": "B"})
	require.NoError(t, err)

	// Insert two jobs of type "task-A" and one job of type "task-B".
	jobA1 := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-A",
		Payload:      payloadA1,
		ScheduledFor: 1630000000,
		ValidUntil:   1630003600,
	}
	jobA2 := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-A",
		Payload:      payloadA2,
		ScheduledFor: 1630000001,
		ValidUntil:   1630003601,
	}
	jobB := store.Job{
		ID:           uuid.New().String(),
		TaskType:     "task-B",
		Payload:      payloadB,
		ScheduledFor: 1630000002,
		ValidUntil:   1630003602,
	}

	require.NoError(t, s.AppendJob(ctx, jobA1))
	time.Sleep(10 * time.Millisecond) // Ensure different created_at timestamps.
	require.NoError(t, s.AppendJob(ctx, jobA2))
	require.NoError(t, s.AppendJob(ctx, jobB))

	// Retrieve all jobs of type "task-A" without deletion.
	jobsA, err := s.GetJobsForType(ctx, "task-A")
	require.NoError(t, err)
	require.Len(t, jobsA, 2)

	// Ensure the jobs are returned in order of creation.
	require.Equal(t, jobA1.ID, jobsA[0].ID)
	require.Equal(t, jobA2.ID, jobsA[1].ID)
	// Check that scheduledFor and validUntil are correct.
	require.Equal(t, jobA1.ScheduledFor, jobsA[0].ScheduledFor)
	require.Equal(t, jobA1.ValidUntil, jobsA[0].ValidUntil)
	require.Equal(t, jobA2.ScheduledFor, jobsA[1].ScheduledFor)
	require.Equal(t, jobA2.ValidUntil, jobsA[1].ValidUntil)

	// Calling GetJobsForType again should return the same jobs.
	jobsAAgain, err := s.GetJobsForType(ctx, "task-A")
	require.NoError(t, err)
	require.Len(t, jobsAAgain, 2)

	// Retrieve jobs for "task-B".
	jobsB, err := s.GetJobsForType(ctx, "task-B")
	require.NoError(t, err)
	require.Len(t, jobsB, 1)
	require.Equal(t, jobB.ID, jobsB[0].ID)
	require.Equal(t, jobB.ScheduledFor, jobsB[0].ScheduledFor)
	require.Equal(t, jobB.ValidUntil, jobsB[0].ValidUntil)
}
