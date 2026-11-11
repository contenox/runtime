package localfileapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentview"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

func AddRoutes(mux *http.ServeMux, service localfileservice.Service) {
	h := &handler{service: service}
	mux.HandleFunc("GET /files", h.list)
	mux.HandleFunc("GET /files/stat", h.stat)
	mux.HandleFunc("GET /files/content", h.content)
	mux.HandleFunc("GET /files/download", h.download)
	mux.HandleFunc("POST /files", h.createFile)
	mux.HandleFunc("PUT /files", h.updateFile)
	mux.HandleFunc("DELETE /files", h.deleteFile)
	mux.HandleFunc("PUT /files/move", h.movePath)
	mux.HandleFunc("POST /folders", h.createFolder)
	mux.HandleFunc("DELETE /folders", h.deleteFolder)
}

type handler struct {
	service localfileservice.Service
	view    *vfs.View
	filters map[string]FileFilter
	hitlFor PolicyEvaluatorFactory
	events  workspaceevents.Service
}

func (h *handler) saved(r *http.Request, entry *localfileservice.Entry, extra map[string]any) {
	if h.events == nil || entry == nil {
		return
	}
	data := map[string]any{"etag": entry.ETag}
	for k, v := range extra {
		data[k] = v
	}
	raw, _ := json.Marshal(data)
	subject := filepath.Join(h.service.Root(), filepath.FromSlash(entry.Path))
	_, _ = h.events.Append(r.Context(), h.service.Root(), workspaceevents.Input{
		Type:    workspaceevents.TypeEditorSaved,
		Source:  workspaceevents.SourceEditor,
		Subject: subject,
		Data:    raw,
	})
}

func writePreconditionRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPreconditionRequired)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": localfileservice.ErrPreconditionRequired.Error(),
		"type":    "precondition_error",
		"code":    "precondition_required",
	}})
}

func writeConflict(w http.ResponseWriter, conflict *localfileservice.ConflictError) {
	w.Header().Set("Content-Type", "application/json")
	if conflict.Current != "" {
		w.Header().Set("ETag", strconv.Quote(conflict.Current))
	}
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message":     conflict.Error(),
		"type":        "conflict_error",
		"code":        "file_changed",
		"path":        conflict.Path,
		"currentEtag": conflict.Current,
	}})
}

type writeFileRequest struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	ContentBase64 string `json:"contentBase64"`
}

type createFolderRequest struct {
	Path string `json:"path"`
}

type moveRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"newPath"`
}

type fileContentResponse struct {
	Path          string                 `json:"path"`
	ETag          string                 `json:"etag"`
	Content       string                 `json:"content"`
	ContentBase64 string                 `json:"contentBase64,omitempty"`
	Encoding      string                 `json:"encoding"`
	Metadata      localfileservice.Entry `json:"metadata"`
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", ".", "Directory path relative to the project root.")
	entries, err := h.service.List(r.Context(), path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}

	filterName := strings.TrimSpace(apiframework.GetQueryParam(r, "filter", "", "Tree view to apply: 'full' (default, raw tree) or 'agent' (the tree as the agent sees it, with per-path access verdicts)."))
	if filterName == "" || filterName == "full" {
		_ = apiframework.Encode(w, r, http.StatusOK, entries)
		return
	}

	filter, ok := h.filters[filterName]
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: unknown filter %q", apiframework.ErrUnprocessableEntity, filterName),
			apiframework.ListOperation)
		return
	}
	if h.view == nil || h.hitlFor == nil {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: filter %q is not available on this endpoint", apiframework.ErrUnprocessableEntity, filterName),
			apiframework.ListOperation)
		return
	}

	policyName := strings.TrimSpace(apiframework.GetQueryParam(r, "policy", "", "HITL policy name to evaluate the agent view against; omitted uses the configured default."))
	ev := agentview.NewEvaluator(h.view, h.hitlFor(policyName), policyName)

	annotated := make([]Entry, len(entries))
	for i, e := range entries {
		annotated[i] = Entry{Entry: e}
	}
	result, err := filter.Apply(r.Context(), annotated, ev)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, result)
}

func (h *handler) stat(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", "", "Path relative to the project root.")
	entry, err := h.service.Stat(r.Context(), path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, entry)
}

func (h *handler) content(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", "", "File path relative to the project root.")
	data, meta, err := h.service.Read(r.Context(), path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	resp := fileContentResponse{
		Path:     meta.Path,
		ETag:     meta.ETag,
		Metadata: *meta,
	}
	w.Header().Set("ETag", strconv.Quote(meta.ETag))
	if looksText(data) {
		resp.Content = string(data)
		resp.Encoding = "utf-8"
	} else {
		resp.ContentBase64 = base64.StdEncoding.EncodeToString(data)
		resp.Encoding = "base64"
	}
	_ = apiframework.Encode(w, r, http.StatusOK, resp)
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", "", "File path relative to the project root.")
	data, meta, err := h.service.Read(r.Context(), path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (h *handler) createFile(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[writeFileRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	data, err := decodeFileContent(req)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	entry, err := h.service.Write(r.Context(), req.Path, data, true)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	h.saved(r, entry, nil)
	w.Header().Set("ETag", strconv.Quote(entry.ETag))
	_ = apiframework.Encode(w, r, http.StatusCreated, entry)
}

func (h *handler) updateFile(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[writeFileRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	data, err := decodeFileContent(req)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	entry, err := h.service.WriteIfMatch(r.Context(), req.Path, data, r.Header.Get("If-Match"))
	if errors.Is(err, localfileservice.ErrPreconditionRequired) {
		writePreconditionRequired(w)
		return
	}
	var conflict *localfileservice.ConflictError
	if errors.As(err, &conflict) {
		writeConflict(w, conflict)
		return
	}
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	h.saved(r, entry, nil)
	w.Header().Set("ETag", strconv.Quote(entry.ETag))
	_ = apiframework.Encode(w, r, http.StatusOK, entry)
}

func (h *handler) createFolder(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[createFolderRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	entry, err := h.service.Mkdir(r.Context(), req.Path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, entry)
}

func (h *handler) movePath(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[moveRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	entry, err := h.service.Move(r.Context(), req.Path, req.NewPath)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, entry)
}

func (h *handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", "", "File path relative to the project root.")
	if err := h.service.Delete(r.Context(), path); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, apiframework.MessageResponse{Message: "path removed"})
}

func (h *handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	path := apiframework.GetQueryParam(r, "path", "", "Folder path relative to the project root.")
	if err := h.service.Delete(r.Context(), path); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, apiframework.MessageResponse{Message: "path removed"})
}

func decodeFileContent(req writeFileRequest) ([]byte, error) {
	if req.ContentBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid contentBase64", apiframework.ErrUnprocessableEntity)
		}
		return data, nil
	}
	return []byte(req.Content), nil
}

func looksText(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}
