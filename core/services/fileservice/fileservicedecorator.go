package fileservice

import (
	"context"
	"fmt"

	"github.com/js402/cate/core/serverops"
)

type activityTrackerDecorator struct {
	fileservice Service
	tracker     serverops.ActivityTracker
}

// WithActivityTracker decorates a FileService implementation with an ActivityTracker.
// It intercepts each method call, using the tracker to report on the operation's
// lifecycle (start, end), outcome (error), and any resulting state changes.
func WithActivityTracker(fileservice Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		fileservice: fileservice,
		tracker:     tracker,
	}
}

func (d *activityTrackerDecorator) CreateFile(ctx context.Context, file *File) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"file",
		"path", file.Path,
		"contentType", file.ContentType,
		"size", fmt.Sprintf("%d", file.Size),
	)
	defer endFn()

	createdFile, opErr := d.fileservice.CreateFile(ctx, file)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(createdFile.ID, nil)
	}

	return createdFile, opErr
}

func (d *activityTrackerDecorator) GetFileByID(ctx context.Context, id string) (*File, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"file",
		"fileID", id,
	)
	defer endFn()

	foundFile, opErr := d.fileservice.GetFileByID(ctx, id)
	if opErr != nil {
		reportErrFn(opErr)
	}
	return foundFile, opErr
}

func (d *activityTrackerDecorator) GetFilesByPath(ctx context.Context, path string) ([]File, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"file",
		"path", path,
	)
	defer endFn()

	files, opErr := d.fileservice.GetFilesByPath(ctx, path)
	if opErr != nil {
		reportErrFn(opErr)
	}
	return files, opErr
}

func (d *activityTrackerDecorator) UpdateFile(ctx context.Context, file *File) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"update",
		"file",
		"fileID", file.ID,
		"path", file.Path,
		"contentType", file.ContentType,
		"size", fmt.Sprintf("%d", file.Size),
	)
	defer endFn()

	updatedFile, opErr := d.fileservice.UpdateFile(ctx, file)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(updatedFile.ID, updatedFile)
	}
	return updatedFile, opErr
}

func (d *activityTrackerDecorator) DeleteFile(ctx context.Context, id string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"file",
		"fileID", id,
	)
	defer endFn()

	opErr := d.fileservice.DeleteFile(ctx, id)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(id, nil)
	}
	return opErr
}

func (d *activityTrackerDecorator) ListAllPaths(ctx context.Context) ([]string, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"list",
		"path",
	)
	defer endFn()

	paths, opErr := d.fileservice.ListAllPaths(ctx)
	if opErr != nil {
		reportErrFn(opErr)
	}
	return paths, opErr
}

func (d *activityTrackerDecorator) CreateFolder(ctx context.Context, path string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"folder",
		"path", path,
	)
	defer endFn()

	folder, opErr := d.fileservice.CreateFolder(ctx, path)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(folder.ID, folder)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) RenameFile(ctx context.Context, fileID, newPath string) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"rename",
		"file",
		"fileID", fileID,
		"newPath", newPath,
	)
	defer endFn()

	file, opErr := d.fileservice.RenameFile(ctx, fileID, newPath)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(file.ID, file)
	}
	return file, opErr
}

func (d *activityTrackerDecorator) RenameFolder(ctx context.Context, folderID, newPath string) (*Folder, error) {
	// Operation: "rename", Subject: "folder"
	// Args: Pass the folder ID and the new path
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"rename",
		"folder",
		"folderID", folderID,
		"newPath", newPath,
	)
	defer endFn()

	folder, opErr := d.fileservice.RenameFolder(ctx, folderID, newPath)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(folder.ID, folder)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.fileservice.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.fileservice.GetServiceGroup()
}

var _ Service = (*activityTrackerDecorator)(nil)
