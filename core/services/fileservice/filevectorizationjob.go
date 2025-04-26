package fileservice

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
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
		ID:        uuid.NewString(),
		Operation: i.operation,
		Subject:   i.subject,
		EntityID:  id,
		TaskType:  "vectorize_" + file.ContentType,
		// Payload:   payload, 	// Note: We don't include payload, it may be very large
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
