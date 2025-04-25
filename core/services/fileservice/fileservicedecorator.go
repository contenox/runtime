package fileservice

import (
	"context"

	"github.com/js402/cate/core/serverops"
)

type activityTrackerDecorator struct {
	fileservice Service
	tracker     serverops.ActivityTracker
}

// WithActivityTracker decorates a FileService implementation with an ActivityTracker.
// It wraps each method call with tracking logic to report errors, side effects, and duration.
//
// This allows clients to plug in arbitrary tracking logic (such as logging, metrics, or tracing)
// without modifying the core service logic.
//
// Example use case:
//
//	trackedService := fileservice.WithActivityTracker(realService, myTracker)
//	trackedService.CreateFile(ctx, file)
func WithActivityTracker(fileservice Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		fileservice: fileservice,
		tracker:     tracker,
	}
}

func (d *activityTrackerDecorator) CreateFile(ctx context.Context, file *File) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.CreateFile", file.Path, file.ContentType, file.Size)
	defer endFn()

	createdFile, opErr := d.fileservice.CreateFile(ctx, file)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("CreateFile" + createdFile.ID)
	}

	return createdFile, opErr
}

func (d *activityTrackerDecorator) GetFileByID(ctx context.Context, id string) (*File, error) {
	reportErrFn, _, endFn := d.tracker.Start("FileService.GetFileByID", id)
	defer endFn()

	foundFile, opErr := d.fileservice.GetFileByID(ctx, id)

	if opErr != nil {
		reportErrFn(opErr)
	}
	return foundFile, opErr
}

func (d *activityTrackerDecorator) GetFilesByPath(ctx context.Context, path string) ([]File, error) {
	reportErrFn, _, endFn := d.tracker.Start("FileService.GetFilesByPath", path)
	defer endFn()

	files, opErr := d.fileservice.GetFilesByPath(ctx, path)

	if opErr != nil {
		reportErrFn(opErr)
	}
	return files, opErr
}

func (d *activityTrackerDecorator) UpdateFile(ctx context.Context, file *File) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.UpdateFile", file.ID, file.Path, file.ContentType, file.Size)
	defer endFn()

	updatedFile, opErr := d.fileservice.UpdateFile(ctx, file)

	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("UpdateFile." + updatedFile.ID)
	}
	return updatedFile, opErr
}

func (d *activityTrackerDecorator) DeleteFile(ctx context.Context, id string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.DeleteFile", id)
	defer endFn()

	opErr := d.fileservice.DeleteFile(ctx, id)

	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("DeleteFile." + id)
	}
	return opErr
}

func (d *activityTrackerDecorator) ListAllPaths(ctx context.Context) ([]string, error) {
	reportErrFn, _, endFn := d.tracker.Start("FileService.ListAllPaths")
	defer endFn()

	paths, opErr := d.fileservice.ListAllPaths(ctx)

	if opErr != nil {
		reportErrFn(opErr)
	}
	return paths, opErr
}

func (d *activityTrackerDecorator) CreateFolder(ctx context.Context, path string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.CreateFolder", path)
	defer endFn()

	folder, opErr := d.fileservice.CreateFolder(ctx, path)

	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("CreateFolder." + folder.ID)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) RenameFile(ctx context.Context, fileID, newPath string) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.RenameFile", fileID, newPath)
	defer endFn()

	file, opErr := d.fileservice.RenameFile(ctx, fileID, newPath)

	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("RenameFile." + file.ID)
	}
	return file, opErr
}

func (d *activityTrackerDecorator) RenameFolder(ctx context.Context, folderID, newPath string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start("FileService.RenameFolder", folderID, newPath)
	defer endFn()

	folder, opErr := d.fileservice.RenameFolder(ctx, folderID, newPath)

	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn("RenameFolder." + folder.ID)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.fileservice.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.fileservice.GetServiceGroup()
}
