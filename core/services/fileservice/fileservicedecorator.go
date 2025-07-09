package fileservice

import (
	"context"
	"fmt"

	"github.com/contenox/runtime-mvp/core/serverops"
)

type activityTrackerDecorator struct {
	fileservice Service
	tracker     serverops.ActivityTracker
}

// sanitizeFile removes sensitive data fields before logging
func sanitizeFile(file *File) *File {
	if file == nil {
		return nil
	}
	return &File{
		ID:          file.ID,
		Path:        file.Path,
		Name:        file.Name,
		ParentID:    file.ParentID,
		Size:        file.Size,
		ContentType: file.ContentType,
		// Explicitly omit the Data field containing file content
	}
}

func (d *activityTrackerDecorator) MoveFile(ctx context.Context, fileID string, newParentID string) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"move",
		"file",
		"fileID", fileID,
		"newParentID", newParentID,
	)
	defer endFn()

	movedFile, opErr := d.fileservice.MoveFile(ctx, fileID, newParentID)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(movedFile.ID, sanitizeFile(movedFile))
	}
	return movedFile, opErr
}

func (d *activityTrackerDecorator) MoveFolder(ctx context.Context, folderID string, newParentID string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"move",
		"folder",
		"folderID", folderID,
		"newParentID", newParentID,
	)
	defer endFn()

	movedFolder, opErr := d.fileservice.MoveFolder(ctx, folderID, newParentID)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(movedFolder.ID, movedFolder)
	}
	return movedFolder, opErr
}

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
		reportChangeFn(createdFile.ID, sanitizeFile(createdFile))
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
		reportChangeFn(updatedFile.ID, sanitizeFile(updatedFile))
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

func (d *activityTrackerDecorator) CreateFolder(ctx context.Context, parentID, name string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"folder",
		"name", name,
	)
	defer endFn()

	folder, opErr := d.fileservice.CreateFolder(ctx, parentID, name)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(folder.ID, folder)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) RenameFile(ctx context.Context, fileID, newName string) (*File, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"rename",
		"file",
		"fileID", fileID,
		"newName", newName,
	)
	defer endFn()

	file, opErr := d.fileservice.RenameFile(ctx, fileID, newName)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(file.ID, sanitizeFile(file))
	}
	return file, opErr
}

func (d *activityTrackerDecorator) RenameFolder(ctx context.Context, folderID, newName string) (*Folder, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"rename",
		"folder",
		"fileID", folderID,
		"newName", newName,
	)
	defer endFn()

	folder, opErr := d.fileservice.RenameFolder(ctx, folderID, newName)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(folder.ID, folder)
	}
	return folder, opErr
}

func (d *activityTrackerDecorator) DeleteFolder(ctx context.Context, folderID string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"delete",
		"folder",
		"fileID", folderID,
	)
	defer endFn()

	opErr := d.fileservice.DeleteFolder(ctx, folderID)
	if opErr != nil {
		reportErrFn(opErr)
	} else {
		reportChangeFn(folderID, nil)
	}
	return opErr
}

func (d *activityTrackerDecorator) GetFolderByID(ctx context.Context, id string) (*Folder, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"file",
		"fileID", id,
	)
	defer endFn()

	foundFile, opErr := d.fileservice.GetFolderByID(ctx, id)
	if opErr != nil {
		reportErrFn(opErr)
	}
	return foundFile, opErr
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.fileservice.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.fileservice.GetServiceGroup()
}

var _ Service = (*activityTrackerDecorator)(nil)
