package fileservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/libs/libdb"
)

type Service struct {
	db libdb.DBManager
}

func New(db libdb.DBManager, config *serverops.Config) *Service {
	return &Service{
		db: db,
	}
}

// File represents a file entity.
type File struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}

// Metadata holds file metadata.
type Metadata struct {
	SpecVersion string `json:"spec_version"`
	Path        string `json:"path"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	FileID      string `json:"file_id"`
}

func (s *Service) CreateFile(ctx context.Context, file *File) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	// Start a transaction.
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}

	// Generate IDs.
	fileID := uuid.NewString()
	blobID := uuid.NewString()

	// Compute SHA-256 hash of the file data.
	hashBytes := sha256.Sum256(file.Data)
	hashString := hex.EncodeToString(hashBytes[:])

	meta := Metadata{
		SpecVersion: "1.0",
		Path:        file.Path,
		Hash:        hashString,
		Size:        int64(len(file.Data)),
		FileID:      fileID,
	}
	bMeta, err := json.Marshal(&meta)
	if err != nil {
		return err
	}

	storeService := store.New(tx)

	// Create blob record.
	blob := &store.Blob{
		ID:   blobID,
		Data: file.Data,
		Meta: bMeta,
	}
	if err = storeService.CreateBlob(ctx, blob); err != nil {
		return fmt.Errorf("failed to create blob: %w", err)
	}

	// Create file record.
	fileRecord := &store.File{
		ID:      fileID,
		Path:    file.Path,
		Type:    file.ContentType,
		Meta:    bMeta,
		BlobsID: blobID,
	}
	if err = storeService.CreateFile(ctx, fileRecord); err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	creatorID, err := serverops.GetIdentity[store.AccessList](ctx)
	if err != nil {
		return fmt.Errorf("failed to get identity: %w", err)
	}
	if creatorID == "" {
		return fmt.Errorf("creator ID is empty")
	}
	// Grant access to the creator.
	accessEntry := &store.AccessEntry{
		ID:         uuid.NewString(),
		Identity:   creatorID,
		Resource:   file.Path,
		Permission: store.PermissionManage,
	}
	if err := storeService.CreateAccessEntry(ctx, accessEntry); err != nil {
		return fmt.Errorf("failed to create access entry: %w", err)
	}

	// Commit the transaction.
	return commit(ctx)
}

func (s *Service) GetFileByID(ctx context.Context, id string) (*File, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return nil, err
	}
	// Start a transaction.
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}

	// Get file record.
	fileRecord, err := store.New(tx).GetFileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckResourceAuthorization[store.AccessList](ctx, fileRecord.Path, store.PermissionView); err != nil {
		return nil, err
	}
	// Get associated blob.
	blob, err := store.New(tx).GetBlobByID(ctx, fileRecord.BlobsID)
	if err != nil {
		return nil, err
	}

	// Reconstruct the File.
	resFile := &File{
		ID:          fileRecord.ID,
		Path:        fileRecord.Path,
		ContentType: fileRecord.Type,
		Data:        blob.Data,
		Size:        int64(len(blob.Data)),
	}

	if err := commit(ctx); err != nil {
		return nil, err
	}
	return resFile, nil
}

func (s *Service) GetFilesByPath(ctx context.Context, path string) ([]File, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return nil, err
	}
	// Start a transaction to fetch files and their blobs.
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}

	fileRecords, err := store.New(tx).GetFilesByPath(ctx, path)
	if err != nil {
		return nil, err
	}

	var files []File
	for _, fr := range fileRecords {
		blob, err := store.New(tx).GetBlobByID(ctx, fr.BlobsID)
		if err != nil {
			return nil, err
		}
		files = append(files, File{
			ID:          fr.ID,
			Path:        fr.Path,
			ContentType: fr.Type,
			//Data:        blob.Data, // Don't include data in response without permission check
			Size: int64(len(blob.Data)),
		})
	}

	if err := commit(ctx); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Service) UpdateFile(ctx context.Context, file *File) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	// Start a transaction.
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}

	// Retrieve the existing file record to get the blob ID.
	existing, err := store.New(tx).GetFileByID(ctx, file.ID)
	if err != nil {
		return err
	}
	if err := serverops.CheckResourceAuthorization[store.AccessList](ctx, existing.Path, store.PermissionEdit); err != nil {
		return err
	}
	blobID := existing.BlobsID

	// Compute new hash and metadata.
	hashBytes := sha256.Sum256(file.Data)
	hashString := hex.EncodeToString(hashBytes[:])
	meta := Metadata{
		SpecVersion: "1.0",
		Path:        file.Path,
		Hash:        hashString,
		Size:        int64(len(file.Data)),
		FileID:      file.ID,
	}
	bMeta, err := json.Marshal(&meta)
	if err != nil {
		return err
	}

	// Update blob record.
	blob := &store.Blob{
		ID:   blobID,
		Data: file.Data,
		Meta: bMeta,
	}
	if err := store.New(tx).DeleteBlob(ctx, blobID); err != nil {
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	if err := store.New(tx).CreateBlob(ctx, blob); err != nil {
		return fmt.Errorf("failed to update blob: %w", err)
	}

	// Update file record.
	updatedFile := &store.File{
		ID:      file.ID,
		Path:    file.Path,
		Type:    file.ContentType,
		Meta:    bMeta,
		BlobsID: blobID,
	}
	if err := store.New(tx).UpdateFile(ctx, updatedFile); err != nil {
		return fmt.Errorf("failed to update file: %w", err)
	}

	return commit(ctx)
}

func (s *Service) DeleteFile(ctx context.Context, id string) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	storeService := store.New(tx)

	// Get file details.
	file, err := storeService.GetFileByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}
	if err := serverops.CheckResourceAuthorization[store.AccessList](ctx, file.Path, store.PermissionManage); err != nil {
		return err
	}
	// Delete associated blob.
	if err := storeService.DeleteBlob(ctx, file.BlobsID); err != nil {
		return fmt.Errorf("failed to delete blob: %w", err)
	}

	// Delete file record.
	if err := storeService.DeleteFile(ctx, id); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	// Remove related access entries.
	if err := storeService.DeleteAccessEntriesByIdentity(ctx, file.Path); err != nil {
		return fmt.Errorf("failed to delete access entries: %w", err)
	}

	return commit(ctx)
}

func (s *Service) ListAllPaths(ctx context.Context) ([]string, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return nil, err
	}
	// Start a transaction.
	tx, commit, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}

	// Retrieve the distinct paths using the store method.
	paths, err := store.New(tx).ListAllPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list all paths: %w", err)
	}

	// Commit the transaction.
	if err := commit(ctx); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Service) GetServiceName() string {
	return "fileservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
