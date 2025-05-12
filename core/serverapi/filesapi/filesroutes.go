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

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/fileservice"
)

const (
	MaxRequestSize      = fileservice.MaxUploadSize + 10*1024
	multipartFormMemory = 8 << 20
	formFieldFile       = "file"
	formFieldPath       = "path"
)

func AddFileRoutes(mux *http.ServeMux, config *serverops.Config, fileService fileservice.Service) {
	f := &fileManager{
		service: fileService,
	}

	mux.HandleFunc("POST /files", f.create)
	mux.HandleFunc("GET /files/{id}", f.getMetadata)
	mux.HandleFunc("PUT /files/{id}", f.update)
	mux.HandleFunc("DELETE /files/{id}", f.delete)
	mux.HandleFunc("GET /files/{id}/download", f.download)
	mux.HandleFunc("GET /files", f.listFiles)
	mux.HandleFunc("POST /folders", f.createFolder)
	mux.HandleFunc("PUT /files/{id}/path", f.renameFile)
	mux.HandleFunc("PUT /folders/{id}/path", f.renameFolder)
}

type fileManager struct {
	service fileservice.Service
}

type fileResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

func mapFileToResponse(f *fileservice.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		Path:        f.Path,
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
	string, // path
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
		return nil, nil, "", "", localErr
	}

	filePart, header, formErr := r.FormFile(formFieldFile)
	if formErr != nil {
		if errors.Is(formErr, http.ErrMissingFile) {
			return nil, nil, "", "", formErr
		}
		localErr := fmt.Errorf("invalid '%s' upload: %w", formFieldFile, formErr)
		return nil, nil, "", "", localErr
	}
	defer filePart.Close()

	// a quick check.
	if header.Size > fileservice.MaxUploadSize {
		return nil, nil, "", "", serverops.ErrFileSizeLimitExceeded
	}
	if header.Size == 0 {
		return nil, nil, "", "", serverops.ErrFileEmpty
	}

	// Reading one extra byte allows us to detect if the original file was larger than the limit.
	limitedReader := io.LimitReader(filePart, fileservice.MaxUploadSize+1)
	fileData, readErr := io.ReadAll(limitedReader)
	if readErr != nil {
		localErr := fmt.Errorf("failed to read file content for '%s': %w", header.Filename, readErr)
		return nil, nil, "", "", localErr
	}

	// If we read more than MaxUploadSize bytes, it means the original stream had more data.
	if int64(len(fileData)) > fileservice.MaxUploadSize {
		return nil, nil, "", "", serverops.ErrFileSizeLimitExceeded
	}
	// We now have the file data, guaranteed to be <= MaxUploadSize bytes.

	detectedMimeType := http.DetectContentType(fileData)

	var resultPath string
	specifiedPath := r.FormValue(formFieldPath)
	if specifiedPath == "" {
		resultPath = header.Filename
	} else {
		resultPath = specifiedPath
	}

	return header, fileData, resultPath, detectedMimeType, nil
}

// create handles the creation of a new file using multipart/form-data.
func (f *fileManager) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	header, fileData, path, mimeType, err := f.processAndReadFileUpload(w, r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	req := fileservice.File{
		Path:        path,
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

	header, fileData, path, mimeType, err := f.processAndReadFileUpload(w, r)
	if err != nil {
		// Pass the raw error to serverops.Error
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	req := fileservice.File{
		ID:          id,
		Path:        path,
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
func (f *fileManager) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if err := f.service.DeleteFile(ctx, id); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, "file removed")
}

type folderResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func mapServiceFileToFileResponse(f *fileservice.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		Path:        f.Path,
		ContentType: f.ContentType,
		Size:        f.Size,
	}
}

func mapFolderToResponse(f *fileservice.Folder) folderResponse {
	return folderResponse{
		ID:   f.ID,
		Path: f.Path,
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
	Path     string `json:"path"`
	ParentID string `json:"parentId"`
}
type pathUpdateRequest struct {
	Path string `json:"path"`
}

func (f *fileManager) createFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req folderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.CreateOperation)
		return
	}
	if req.Path == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'path' in request body"), serverops.CreateOperation)
		return
	}

	folder, err := f.service.CreateFolder(ctx, req.ParentID, req.Path)
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

	var req pathUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}
	if req.Path == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'path' in request body"), serverops.UpdateOperation)
		return
	}

	folder, err := f.service.RenameFolder(ctx, id, req.Path)
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

	var req pathUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("invalid request body: %w", err), serverops.UpdateOperation)
		return
	}
	if req.Path == "" {
		_ = serverops.Error(w, r, fmt.Errorf("missing 'path' in request body"), serverops.UpdateOperation)
		return
	}

	file, err := f.service.RenameFile(ctx, id, req.Path)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapServiceFileToFileResponse(file))
}
