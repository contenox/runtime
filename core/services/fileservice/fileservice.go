package fileservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
)

const MaxUploadSize = 1 * 1024 * 1024
const MaxFilesRowCount = 50000

type Service interface {
	CreateFile(ctx context.Context, file *File) (*File, error)
	GetFileByID(ctx context.Context, id string) (*File, error)
	GetFilesByPath(ctx context.Context, path string) ([]File, error)
	UpdateFile(ctx context.Context, file *File) (*File, error)
	DeleteFile(ctx context.Context, id string) error
	ListAllPaths(ctx context.Context) ([]string, error)
	CreateFolder(ctx context.Context, path string) (*Folder, error)
	RenameFile(ctx context.Context, fileID, newPath string) (*File, error)
	RenameFolder(ctx context.Context, folderID, newPath string) (*Folder, error)
	serverops.ServiceMeta
}

var _ Service = (*service)(nil)

type service struct {
	db libdb.DBManager
}

func New(db libdb.DBManager, config *serverops.Config) Service {
	return &service{
		db: db,
	}
}

// File represents a file entity.
type File struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
}

type Folder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Metadata holds file metadata.
type Metadata struct {
	SpecVersion string `json:"specVersion"`
	Path        string `json:"path"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	FileID      string `json:"fileId"`
}

func (s *service) CreateFile(ctx context.Context, file *File) (*File, error) {
	_, err := validateContentType(file.ContentType)
	if err != nil {
		return nil, err
	}

	cleanedPath, err := sanitizePath(file.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	file.Path = cleanedPath

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
		return nil, err
	}

	// Create blob record.
	blob := &store.Blob{
		ID:   blobID,
		Data: file.Data,
		Meta: bMeta,
	}
	if file.Size > MaxUploadSize {
		return nil, serverops.ErrFileSizeLimitExceeded
	}
	// Start a transaction.
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return nil, err
	}
	storeInstance := store.New(tx)
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionManage); err != nil {
		return nil, err
	}
	err = storeInstance.EnforceMaxFileCount(ctx, MaxFilesRowCount)
	if err != nil {
		err := fmt.Errorf("too many files in the system: %w", err)
		fmt.Printf("SERVER ERROR: file creation blocked: limit reached (%d max) %v", err, MaxFilesRowCount)
		return nil, err
	}

	if err = storeInstance.CreateBlob(ctx, blob); err != nil {
		return nil, fmt.Errorf("failed to create blob: %w", err)
	}

	// Create file record.
	fileRecord := &store.File{
		ID:      fileID,
		Path:    file.Path,
		Type:    file.ContentType,
		Meta:    bMeta,
		BlobsID: blobID,
	}
	if err = storeInstance.CreateFile(ctx, fileRecord); err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	creatorID, err := serverops.GetIdentity(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get identity: %w", err)
	}
	if creatorID == "" {
		return nil, fmt.Errorf("creator ID is empty")
	}
	// Grant access to the creator.
	accessEntry := &store.AccessEntry{
		ID:         uuid.NewString(),
		Identity:   creatorID,
		Resource:   fileID,
		Permission: store.PermissionManage,
	}
	if err := storeInstance.CreateAccessEntry(ctx, accessEntry); err != nil {
		return nil, fmt.Errorf("failed to create access entry: %w", err)
	}
	resFiles, err := s.getFileByID(ctx, tx, fileID)
	if err != nil {
		return nil, err
	}
	err = commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return resFiles, nil
}

func (s *service) GetFileByID(ctx context.Context, id string) (*File, error) {
	// Start a transaction.
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}

	if err := serverops.CheckResourceAuthorization(ctx, store.New(tx), serverops.ResourceArgs{
		Resource:           id,
		RequiredPermission: store.PermissionView,
		ResourceType:       "files",
	}); err != nil {
		return nil, fmt.Errorf("failed to authorize resource: %w", err)
	}
	resFile, err := s.getFileByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := commit(ctx); err != nil {
		return nil, err
	}
	return resFile, nil
}

func (s *service) getFileByID(ctx context.Context, tx libdb.Exec, id string) (*File, error) {
	// Get file record.
	fileRecord, err := store.New(tx).GetFileByID(ctx, id)
	if err != nil {
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

	return resFile, nil
}

func (s *service) GetFilesByPath(ctx context.Context, path string) ([]File, error) {
	// Start a transaction to fetch files and their blobs.
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	fileRecords, err := store.New(tx).ListFilesByPath(ctx, path)
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

func (s *service) UpdateFile(ctx context.Context, file *File) (*File, error) {
	_, err := validateContentType(file.ContentType)
	if err != nil {
		return nil, err
	}

	cleanedPath, err := sanitizePath(file.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	file.Path = cleanedPath

	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer rTx()
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	existing, err := store.New(tx).GetFileByID(ctx, file.ID)
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckResourceAuthorization(ctx, store.New(tx), serverops.ResourceArgs{
		Resource:           existing.ID,
		ResourceType:       "files",
		RequiredPermission: store.PermissionEdit,
	}); err != nil {
		return nil, err
	}
	blobID := existing.BlobsID

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
		return nil, err
	}

	if err := store.New(tx).DeleteBlob(ctx, blobID); err != nil {
		return nil, fmt.Errorf("failed to delete old blob: %w", err)
	}
	if err := store.New(tx).CreateBlob(ctx, &store.Blob{ID: blobID, Data: file.Data, Meta: bMeta}); err != nil {
		return nil, fmt.Errorf("failed to create new blob: %w", err)
	}

	updated := &store.File{
		ID:      file.ID,
		Path:    file.Path,
		Type:    file.ContentType,
		Meta:    bMeta,
		BlobsID: blobID,
	}
	if err := store.New(tx).UpdateFile(ctx, updated); err != nil {
		return nil, fmt.Errorf("failed to update file record: %w", err)
	}

	res, err := s.getFileByID(ctx, tx, file.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload file: %w", err)
	}
	if err := commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return res, nil
}

func (s *service) DeleteFile(ctx context.Context, id string) error {
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return err
	}
	storeInstance := store.New(tx)

	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionManage); err != nil {
		return err
	}
	// Get file details.
	file, err := storeInstance.GetFileByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}
	if err := serverops.CheckResourceAuthorization(ctx, store.New(tx), serverops.ResourceArgs{
		ResourceType:       "files",
		Resource:           file.ID,
		RequiredPermission: store.PermissionManage,
	}); err != nil {
		return err
	}
	// Delete associated blob.
	if err := storeInstance.DeleteBlob(ctx, file.BlobsID); err != nil {
		return fmt.Errorf("failed to delete blob: %w", err)
	}

	// Delete file record.
	if err := storeInstance.DeleteFile(ctx, id); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	// Remove related access entries.
	if err := storeInstance.DeleteAccessEntriesByResource(ctx, id); err != nil {
		return fmt.Errorf("failed to delete access entries: %w", err)
	}

	return commit(ctx)
}

func (s *service) ListAllPaths(ctx context.Context) ([]string, error) {
	// Start a transaction.
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return nil, err
	}
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	// Retrieve the distinct paths using the store method.
	paths, err := store.New(tx).ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list all paths: %w", err)
	}

	// Commit the transaction.
	if err := commit(ctx); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *service) CreateFolder(ctx context.Context, path string) (*Folder, error) {
	cleanedPath, err := sanitizePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Generate folder ID
	folderID := uuid.NewString()

	// Create metadata
	meta := Metadata{
		SpecVersion: "1.0",
		Path:        cleanedPath,
		FileID:      folderID,
		// Hash and Size are omitted for folders
	}
	bMeta, err := json.Marshal(&meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Start transaction
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer func() {
		if err := rTx(); err != nil {
			log.Println("failed to rollback transaction", err)
		}
	}()
	if err != nil {
		return nil, err
	}

	storeInstance := store.New(tx)
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionManage); err != nil {
		return nil, err
	}

	// Enforce max file count (includes folders)
	if err := storeInstance.EnforceMaxFileCount(ctx, MaxFilesRowCount); err != nil {
		return nil, fmt.Errorf("too many files in the system: %w", err)
	}

	// Create folder record
	folderRecord := &store.File{
		ID:       folderID,
		Path:     cleanedPath,
		Meta:     bMeta,
		IsFolder: true,
	}

	if err := storeInstance.CreateFile(ctx, folderRecord); err != nil {
		return nil, fmt.Errorf("failed to create folder: %w", err)
	}

	if err := commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &Folder{
		ID:   folderID,
		Path: cleanedPath,
	}, nil
}

func (s *service) RenameFile(ctx context.Context, fileID, newPath string) (*File, error) {
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer rTx()
	if err != nil {
		return nil, err
	}
	storeInstance := store.New(tx)

	if err := serverops.CheckResourceAuthorization(ctx, storeInstance, serverops.ResourceArgs{
		ResourceType:       "files",
		Resource:           fileID,
		RequiredPermission: store.PermissionEdit,
	}); err != nil {
		return nil, err
	}
	fileRecord, err := storeInstance.GetFileByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	if fileRecord.IsFolder {
		return nil, fmt.Errorf("target is a folder, use RenameFolder instead")
	}

	cleanedPath, err := sanitizePath(newPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	existing, err := storeInstance.ListFilesByPath(ctx, cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to check path availability: %w", err)
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("path '%s' already exists", cleanedPath)
	}
	if err := storeInstance.UpdateFilePath(ctx, fileID, cleanedPath); err != nil {
		return nil, fmt.Errorf("failed to rename file: %w", err)
	}

	if err := commit(ctx); err != nil {
		return nil, err
	}

	return s.GetFileByID(ctx, fileID)
}

func (s *service) RenameFolder(ctx context.Context, folderID, newPath string) (*Folder, error) {
	// Start transaction
	tx, commit, rTx, err := s.db.WithTransaction(ctx)
	defer rTx()
	if err != nil {
		return nil, err
	}
	storeInstance := store.New(tx)
	// Check service-level manage permission
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionManage); err != nil {
		return nil, err
	}

	// Get the folder
	folderRecord, err := storeInstance.GetFileByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("folder not found: %w", err)
	}
	if !folderRecord.IsFolder {
		return nil, fmt.Errorf("target is not a folder")
	}
	oldPath := folderRecord.Path

	// Sanitize and validate new path
	cleanedPath, err := sanitizePath(newPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Check for existing file/folder at new path
	existing, err := storeInstance.ListFilesByPath(ctx, cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to check path availability: %w", err)
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("path '%s' already exists", cleanedPath)
	}

	// Update folder's own path
	if err := storeInstance.UpdateFilePath(ctx, folderID, cleanedPath); err != nil {
		return nil, fmt.Errorf("failed to rename folder: %w", err)
	}

	// List all files under the old folder path (prefix match)
	descendants, err := storeInstance.ListFilesByPath(ctx, oldPath+"/%")
	if err != nil {
		return nil, fmt.Errorf("failed to list folder contents: %w", err)
	}

	// Prepare bulk updates (ID -> new path)
	updates := make(map[string]string)
	for _, file := range descendants {
		newFilePath := strings.Replace(file.Path, oldPath, cleanedPath, 1)
		updates[file.ID] = newFilePath
	}

	// Apply bulk updates
	if err := storeInstance.BulkUpdateFilePaths(ctx, updates); err != nil {
		return nil, fmt.Errorf("failed to update descendant paths: %w", err)
	}

	// Commit transaction
	if err := commit(ctx); err != nil {
		return nil, err
	}

	return &Folder{
		ID:   folderID,
		Path: cleanedPath,
	}, nil
}

func (s *service) GetServiceName() string {
	return "fileservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}

func detectMimeTee(r io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	tee := io.TeeReader(r, bytes.NewBuffer(buf[:0]))
	_, err := io.ReadFull(tee, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}
	mimeType := http.DetectContentType(buf)

	// Rebuild a combined reader: first the sniffed bytes, then the rest
	combined := io.MultiReader(bytes.NewReader(buf), r)
	return mimeType, combined, nil
}

func detectMimeTypeFromReader(r io.Reader) (string, []byte, error) {
	buffer := make([]byte, 512)
	n, err := r.Read(buffer)
	if err != nil && err != io.EOF {
		return "", nil, err
	}

	mimeType := http.DetectContentType(buffer[:n])

	// reassemble the remaining content
	remaining, err := io.ReadAll(r)
	if err != nil {
		return "", nil, err
	}

	// Combine the sniffed part and the rest
	fullContent := append(buffer[:n], remaining...)
	return mimeType, fullContent, nil
}

var allowedMimeTypes = map[string]bool{
	"text/plain":       true,
	"application/json": true,
	"application/pdf":  true,
}

func validateContentType(contentType string) (mediaType string, err error) {
	mediaType, _, err = mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("invalid content-type header %q: %w", contentType, err)
	}
	if !allowedMimeTypes[mediaType] {
		return "", fmt.Errorf("content type %q is not allowed", mediaType)
	}
	return mediaType, nil
}

func sanitizePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("path contains parent directory traversal")
	}
	return cleaned, nil
}
