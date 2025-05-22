package fileservice

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
)

type fileVectorizationJobCreator struct {
	dbinstance libdb.DBManager
}

// Represents an ongoing file-related operation
type fileActivityInstance struct {
	operation string
	subject   string
	kvArgs    []any
	err       error
}

func (f *fileVectorizationJobCreator) ReportError(ctx context.Context, i *fileActivityInstance, err error) {
	if err == nil {
		return
	}
	i.err = err
}

func (i *fileActivityInstance) CreateJob(ctx context.Context, id string, data any) (*store.Job, error) {
	if data == nil {
		return nil, nil
	}
	file, ok := data.(*File)
	if !ok {
		return nil, nil
	}
	if file.ContentType == "" {
		return nil, fmt.Errorf("file content type is empty")
	}
	task := &store.Job{
		ID:         uuid.NewString(),
		Operation:  i.operation,
		Subject:    i.subject,
		EntityID:   id,
		EntityType: store.ResourceTypeFile,
		TaskType:   "vectorize_" + file.ContentType,
		Payload:    []byte("{}"), // Note: We don't include data, it may be very large
	}
	return task, nil
}

// Start an activity tracker session
func (f *fileVectorizationJobCreator) Start(ctx context.Context, operation string, subject string, kvArgs ...any) (reportErr func(err error), reportChange func(id string, data any), end func()) {
	instance := &fileActivityInstance{
		operation: operation,
		subject:   subject,
		kvArgs:    kvArgs,
	}

	reportErr = func(err error) {
		f.ReportError(ctx, instance, err)
	}
	var job *store.Job
	var err error
	reportChange = func(id string, data any) {
		job, err = instance.CreateJob(ctx, id, data)
		if err != nil {
			fmt.Printf("Error reporting change in activity %s on %s: %v\n", instance.operation, instance.subject, err)
		}
		return
	}

	end = func() {
		if instance.err != nil {
			fmt.Printf("Error in activity %s on %s: %v\n", instance.operation, instance.subject, instance.err)
			return
		}

		// Handle job deletion for file deletions
		if instance.operation == "delete" && instance.subject == "file" {
			var fileID string
			for i := 0; i < len(instance.kvArgs); i += 2 {
				if i+1 >= len(instance.kvArgs) {
					break
				}
				key, ok := instance.kvArgs[i].(string)
				if !ok || key != "fileID" {
					continue
				}
				if id, ok := instance.kvArgs[i+1].(string); ok {
					fileID = id
					break
				}
			}
			if fileID != "" {
				tx, com, r, err := f.dbinstance.WithTransaction(ctx)
				defer r()
				if err != nil {
					fmt.Printf("Error deleting jobs for file %s: %v\n", fileID, err)
					return
				}
				err = store.New(tx).DeleteJobsByEntity(ctx, fileID, store.ResourceTypeFile)
				if err != nil {
					fmt.Printf("Error deleting jobs for file %s: %v\n", fileID, err)
				}
				err = store.New(tx).DeleteLeasedJobs(ctx, fileID, store.ResourceTypeFile)
				if err != nil {
					fmt.Printf("Error deleting jobs for file %s: %v\n", fileID, err)
				}
				err = com(ctx)
				if err != nil {
					fmt.Printf("Error deleting jobs for file %s: %v\n", fileID, err)
				}
			}
		}

		if job == nil {
			return
		}

		tx := f.dbinstance.WithoutTransaction()
		err := store.New(tx).AppendJob(ctx, *job)
		if err != nil {
			fmt.Printf("Error appending job to database: %v\n", err)
		}
	}

	return reportErr, reportChange, end
}

func NewFileVectorizationJobCreator(dbinstance libdb.DBManager) serverops.ActivityTracker {
	return &fileVectorizationJobCreator{dbinstance: dbinstance}
}
