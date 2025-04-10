package filesapi

import (
	"net/http"

	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/services/fileservice"
)

func AddFileRoutes(mux *http.ServeMux, config *serverops.Config, fileService *fileservice.Service) {
	f := &fileManager{service: fileService}

	mux.HandleFunc("POST /files", f.create)
	mux.HandleFunc("GET /files/", f.get)       // expects GET /files/{id}
	mux.HandleFunc("PUT /files/", f.update)    // expects PUT /files/{id}
	mux.HandleFunc("DELETE /files/", f.delete) // expects DELETE /files/{id}

	mux.HandleFunc("GET /files/paths", f.listPaths)
}

type fileManager struct {
	service *fileservice.Service
}

type fileResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
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

func (f *fileManager) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, err := serverops.Decode[fileservice.File](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	if err := f.service.CreateFile(ctx, &req); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, mapFileToResponse(&req))
}

func (f *fileManager) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	file, err := f.service.GetFileByID(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapFileToResponse(file))
}

func (f *fileManager) update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	req, err := serverops.Decode[fileservice.File](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	// Ensure the file ID in the request matches the URL parameter.
	req.ID = id

	if err := f.service.UpdateFile(ctx, &req); err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	updated, err := f.service.GetFileByID(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, mapFileToResponse(updated))
}

func (f *fileManager) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if err := f.service.DeleteFile(ctx, id); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (f *fileManager) listPaths(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	paths, err := f.service.ListAllPaths(ctx)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, paths)
}
