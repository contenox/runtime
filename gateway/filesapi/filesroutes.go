package filesapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/fileservice"
)

const (
	MaxRequestSize      = fileservice.MaxUploadSize + 10*1024
	multipartFormMemory = 8 << 20
	formFieldFile       = "file"
	formFieldName       = "name"
	formFieldParent     = "parentid"
)

// AddFileRoutes sets up the routes for file and folder operations.
func AddFileRoutes(mux *http.ServeMux, config *serverops.Config, fileService fileservice.Service) {
	f := &fileManager{
		service: fileService,
	}

	// File operations
	mux.HandleFunc("POST /files", f.create)                // Create a new file
	mux.HandleFunc("GET /files/{id}", f.getMetadata)       // Get file metadata
	mux.HandleFunc("PUT /files/{id}", f.update)            // Update file
	mux.HandleFunc("DELETE /files/{id}", f.deleteFile)     // Delete a file
	mux.HandleFunc("GET /files/{id}/download", f.download) // Download file content
	mux.HandleFunc("PUT /files/{id}/name", f.renameFile)   // Rename a file
	mux.HandleFunc("PUT /files/{id}/move", f.moveFile)     // Move a file

	// Folder operations
	mux.HandleFunc("POST /folders", f.createFolder)          // Create a new folder
	mux.HandleFunc("PUT /folders/{id}/name", f.renameFolder) // Rename a folder
	mux.HandleFunc("DELETE /folders/{id}", f.deleteFolder)   // Delete a folder
	mux.HandleFunc("PUT /folders/{id}/move", f.moveFolder)   // Move a folder

	// Listing operations (can list both files and folders)
	mux.HandleFunc("GET /files", f.listFiles) // List files/folders by path
}

type fileManager struct {
	service fileservice.Service
}

type fileResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	ContentType string `json:"contentType,omitempty"` // omitempty for folders
	Size        int64  `json:"size"`                  // Will be 0 for folders if mapped directly
}

func mapFileToResponse(f *fileservice.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		Path:        f.Path,
		Name:        f.Name,
		ContentType: f.ContentType,
		Size:        f.Size,
	}
}

// It validates the request, size, and MIME type. Reads the file content into memory,
// ensuring not to read more than MaxUploadSize bytes even if headers are manipulated.
// Returns file header, full file data ([]byte), path, detected mimeType, and error using unnamed returns.
func (f *fileManager) processAndReadFileUpload(w http.ResponseWriter, r *http.Request) (
	*multipart.FileHeader, // header
	[]byte, // fileData
	string, // name
	string, //parent
	string, // mimeType
	error, // err
) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestSize)

	if parseErr := r.ParseMultipartForm(multipartFormMemory); parseErr != nil {
		var localErr error
		var maxBytesErr *http.MaxBytesError
		if errors.As(parseErr, &maxBytesErr) {
			localErr = fmt.Errorf("request body too large (limit %d bytes): %w", maxBytesErr.Limit, parseErr)
		} else if errors.Is(parseErr, http.ErrNotMultipart) {
			localErr = fmt.Errorf("invalid request format (not multipart): %w", parseErr)
		} else {
			localErr = fmt.Errorf("failed to parse multipart form: %w", parseErr)
		}
		return nil, nil, "", "", "", localErr
	}

	filePart, header, formErr := r.FormFile(formFieldFile)
	if formErr != nil {
		if errors.Is(formErr, http.ErrMissingFile) {
			return nil, nil, "", "", "", formErr
		}
		localErr := fmt.Errorf("invalid '%s' upload: %w", formFieldFile, formErr)
		return nil, nil, "", "", "", localErr
	}
	defer filePart.Close()

	// a quick check.
	if header.Size > fileservice.MaxUploadSize {
		return nil, nil, "", "", "", serverops.ErrFileSizeLimitExceeded
	}
	if header.Size == 0 {
		return nil, nil, "", "", "", serverops.ErrFileEmpty
	}

	// Reading one extra byte allows us to detect if the original file was larger than the limit.
	limitedReader := io.LimitReader(filePart, fileservice.MaxUploadSize+1)
	fileData, readErr := io.ReadAll(limitedReader)
	if readErr != nil {
		localErr := fmt.Errorf("failed to read file content for '%s': %w", header.Filename, readErr)
		return nil, nil, "", "", "", localErr
	}

	// If we read more than MaxUploadSize bytes, it means the original stream had more data.
	if int64(len(fileData)) > fileservice.MaxUploadSize {
		return nil, nil, "", "", "", serverops.ErrFileSizeLimitExceeded
	}
	// We now have the file data, guaranteed to be <= MaxUploadSize bytes.
	detectedMimeType := http.DetectContentType(fileData)

	var resultName string
	specifiedName := r.FormValue(formFieldName)
	if specifiedName == "" {
		resultName = header.Filename
	} else {
		resultName = specifiedName
	}
	parentID := r.FormValue(formFieldParent)

	return header, fileData, resultName, parentID, detectedMimeType, nil
}

// create handles the creation of a new file using multipart/form-data.
func (f *fileManager) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	header, fileData, name, parentID, mimeType, err := f.processAndReadFileUpload(w, r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	req := fileservice.File{
		Name:        name,
		ParentID:    parentID,
		ContentType: mimeType,
		Data:        fileData,
		Size:        header.Size,
	}

	file, err := f.service.CreateFile(ctx, &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, file)
}

// getMetadata - No change needed
func (f *fileManager) getMetadata(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	file, err := f.service.GetFileByID(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, mapFileToResponse(file))
}

// update handles updating an existing file using multipart/form-data.
func (f *fileManager) update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	header, fileData, _, parentID, mimeType, err := f.processAndReadFileUpload(w, r)
	if err != nil {
		// Pass the raw error to serverops.Error
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	req := fileservice.File{
		ID:          id,
		ParentID:    parentID,
		ContentType: mimeType,
		Data:        fileData,
		Size:        header.Size,
	}

	file, err := f.service.UpdateFile(ctx, &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, file)
}

// delete - No change needed
func (f *fileManager) deleteFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if err := f.service.DeleteFile(ctx, id); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, map[string]string{"message": "file removed"})
}

type folderResponse struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

func mapServiceFileToFileResponse(f *fileservice.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		Path:        f.Path,
		Name:        f.Name,
		ContentType: f.ContentType,
		Size:        f.Size,
	}
}

func mapFolderToResponse(f *fileservice.Folder) folderResponse {
	return folderResponse{
		ID:       f.ID,
		Path:     f.Path,
		Name:     f.Name,
		ParentID: f.ParentID,
	}
}

func (f *fileManager) listFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pathFilter := url.QueryEscape(r.URL.Query().Get("path"))
	var err error

	files, err := f.service.GetFilesByPath(ctx, pathFilter)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, files)
}

func (f *fileManager) download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	skip := r.URL.Query().Get("skip")

	file, err := f.service.GetFileByID(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}
	if file == nil {
		_ = serverops.Error(w, r, fmt.Errorf("file with id '%s' not found", id), serverops.GetOperation)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	sanitizedFilename := strconv.Quote(file.Path)
	if skip != "true" {
		w.Header().Set("Content-Disposition", "attachment; filename="+sanitizedFilename)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))

	_, copyErr := bytes.NewReader(file.Data).WriteTo(w)
	if copyErr != nil {
		// Can't do much here if writing to response fails midway
	}
}

type folderCreateRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
}
type nameUpdateRequest struct {
	Name string `json:"name"`
}

func (f *fileManager) createFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req folderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.CreateOperation)
		return
	}
	if req.Name == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'name' in request body"), serverops.CreateOperation)
		return
	}

	folder, err := f.service.CreateFolder(ctx, req.ParentID, req.Name)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, mapFolderToResponse(folder))
}

func (f *fileManager) renameFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing folder ID in path"), serverops.UpdateOperation)
		return
	}

	var req nameUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}
	if req.Name == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'name' in request body"), serverops.UpdateOperation)
		return
	}

	folder, err := f.service.RenameFolder(ctx, id, req.Name)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapFolderToResponse(folder))
}

func (f *fileManager) renameFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing file ID in path"), serverops.UpdateOperation)
		return
	}

	var req nameUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}
	if req.Name == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'name' in request body"), serverops.UpdateOperation)
		return
	}

	file, err := f.service.RenameFile(ctx, id, req.Name)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapServiceFileToFileResponse(file))
}

func (f *fileManager) deleteFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing folder ID in path"), serverops.DeleteOperation)
		return
	}

	err := f.service.DeleteFolder(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, map[string]string{"message": "folder removed"})
}

type moveRequest struct {
	NewParentID string `json:"newParentId"`
}

func (f *fileManager) moveFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileID := r.PathValue("id")
	if fileID == "" {
		_ = serverops.Error(w, r, errors.New("missing file ID in path"), serverops.UpdateOperation)
		return
	}

	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}
	movedFile, err := f.service.MoveFile(ctx, fileID, req.NewParentID)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapServiceFileToFileResponse(movedFile))
}

func (f *fileManager) moveFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folderID := r.PathValue("id")
	if folderID == "" {
		_ = serverops.Error(w, r, errors.New("missing folder ID in path"), serverops.UpdateOperation)
		return
	}

	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}

	movedFolder, err := f.service.MoveFolder(ctx, folderID, req.NewParentID)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation) // Or a more specific "MoveOperation"
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapFolderToResponse(movedFolder))
}
