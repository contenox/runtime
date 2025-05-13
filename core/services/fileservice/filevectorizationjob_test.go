package fileservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileVectorizationJob(t *testing.T) {
	ctx := context.Background()

	dbInstance, fileService, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()

	// Attach the file vectorization job creator
	fileService = fileservice.WithActivityTracker(fileService, fileservice.NewFileVectorizationJobCreator(dbInstance))

	t.Run("CreateFile_TriggersVectorizationJob", func(t *testing.T) {
		testFile := &fileservice.File{
			Name:        "vectorize_me.txt",
			ContentType: "text/plain",
			Data:        []byte("some data to vectorize"),
		}

		createdFile, err := fileService.CreateFile(ctx, testFile)
		if err != nil {
			t.Fatalf("CreateFile failed: %v", err)
		}

		jobs, err := store.New(dbInstance.WithoutTransaction()).PopAllJobs(ctx)
		if err != nil {
			t.Fatalf("Failed to list jobs: %v", err)
		}
		if len(jobs) != 1 {
			t.Errorf("Expected 1 job, got %d", len(jobs))
		}
		found := false
		for _, job := range jobs {
			if job.EntityID == createdFile.ID && job.TaskType == "vectorize_text/plain" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("Expected vectorization job for file ID %s not found", createdFile.ID)
		}
	})

	t.Run("CreateFile_EmptyContentType_ReportsError", func(t *testing.T) {
		testFile := &fileservice.File{
			Name:        "empty_content_type.txt",
			ContentType: "", // This should not create a Job
			Data:        []byte("data"),
		}

		_, err := fileService.CreateFile(ctx, testFile)
		if err == nil {
			t.Fatal("Expected error when creating file with empty content type, got nil")
		}

		jobs, err := store.New(dbInstance.WithoutTransaction()).PopAllJobs(ctx)
		if err != nil {
			t.Fatalf("Failed to list jobs: %v", err)
		}

		for _, job := range jobs {
			if job.TaskType == "vectorize_" {
				t.Errorf("Unexpected job with empty task type found: %+v", job)
			}
		}
	})
	t.Run("DeleteFile_CleansUpJobs", func(t *testing.T) {
		// Create test file with vectorization job
		testFile := &fileservice.File{
			Name:        "cleanup_test.txt",
			ContentType: "text/plain",
			Data:        []byte("data"),
		}
		createdFile, err := fileService.CreateFile(ctx, testFile)
		require.NoError(t, err, "CreateFile failed")

		// Verify job creation
		storeInstance := store.New(dbInstance.WithoutTransaction())
		jobs, err := storeInstance.PopAllJobs(ctx)
		require.NoError(t, err, "Failed to get jobs")
		require.Len(t, jobs, 1, "Expected 1 job after creation")

		// Manually create a leased job for testing
		leasedJob := &store.Job{
			ID:         "leased-job-1",
			EntityID:   createdFile.ID,
			EntityType: store.ResourceTypeFile,
			TaskType:   "vectorize_text/plain",
			Payload:    []byte("{}"),
		}
		err = storeInstance.AppendLeasedJob(ctx, *leasedJob, 1*time.Hour, "test-leaser")
		require.NoError(t, err, "Failed to create leased job")

		// Delete the file
		err = fileService.DeleteFile(ctx, createdFile.ID)
		require.NoError(t, err, "DeleteFile failed")

		// Verify job cleanup
		t.Run("regular_jobs_cleaned", func(t *testing.T) {
			remainingJobs, err := storeInstance.PopAllJobs(ctx)
			require.NoError(t, err, "Failed to check jobs")
			assert.Empty(t, remainingJobs, "Jobs should be deleted")
		})

		t.Run("leased_jobs_cleaned", func(t *testing.T) {
			_, err := storeInstance.GetLeasedJob(ctx, leasedJob.ID)
			require.Error(t, err, "Leased job should be deleted")
			assert.Contains(t, err.Error(), "not found", "Should return not found error")
		})

		t.Run("entity_jobs_removed", func(t *testing.T) {
			// Verify using the store's entity cleanup method
			remaining, err := storeInstance.GetJobsForType(ctx, "vectorize_text/plain")
			require.NoError(t, err, "Failed to check jobs by type")

			for _, job := range remaining {
				assert.NotEqual(t, createdFile.ID, job.EntityID,
					"Found orphaned job for deleted file: %s", job.ID)
			}
		})
	})

}
