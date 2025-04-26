package fileservice_test

import (
	"context"
	"testing"

	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/fileservice"
)

func TestFileVectorizationJob(t *testing.T) {
	ctx := context.Background()

	dbInstance, fileService, cleanup := setupFileServiceTestEnv(ctx, t)
	defer cleanup()

	// Attach the file vectorization job creator
	fileService = fileservice.WithActivityTracker(fileService, fileservice.NewFileVectorizationJobCreator(dbInstance))

	t.Run("CreateFile_TriggersVectorizationJob", func(t *testing.T) {
		testFile := &fileservice.File{
			Path:        "vectorize_me.txt",
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
			Path:        "empty_content_type.txt",
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
}
