package workspacegithub

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/google/go-github/v58/github"
)

func New(store runtimetypes.Store, workspaceID string, roots workspaceevents.Service) Service {
	return Service{Store: store, Scope: func(root string) string { return workspaceevents.Scope(workspaceID, root) }, GrantedRoot: roots.GrantedRoot}
}

func (s Service) AddRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/github", s.handle)
	mux.HandleFunc("PUT /workspace/github", s.handle)
	mux.HandleFunc("DELETE /workspace/github", s.handle)
	mux.HandleFunc("GET /workspace/github/{view}", s.handle)
	mux.HandleFunc("POST /workspace/github/reviews", s.handle)
}

func (s Service) handle(w http.ResponseWriter, r *http.Request) {
	data, err := s.request(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status := http.StatusUnprocessableEntity
		var upstream *github.ErrorResponse
		if errors.As(err, &upstream) && upstream.Response != nil {
			status = upstream.Response.StatusCode
		}
		if errors.Is(err, ErrStale) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(data)
}
func (s Service) request(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	q := r.URL.Query()
	if q.Get("root") == "" {
		return nil, errors.New("workspace is required")
	}
	root, err := s.GrantedRoot(q.Get("root"))
	if err != nil {
		return nil, err
	}
	if r.Method == http.MethodGet && r.PathValue("view") == "" {
		return s.State(ctx, root)
	}
	if r.Method == http.MethodDelete {
		return true, s.Store.DeleteWorkspaceKV(ctx, s.Scope(root), "github.repository")
	}
	if r.Method == http.MethodGet {
		id, err := strconv.ParseInt(q.Get("repoId"), 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid repository ID")
		}
		number, _ := strconv.Atoi(q.Get("number"))
		return s.Read(ctx, root, id, r.PathValue("view"), number, q.Get("cursor"))
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return nil, errors.New("JSON request required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if r.Method == http.MethodPut {
		var req struct {
			Remote
			Credential string `json:"credential"`
		}
		if err := decoder.Decode(&req); err != nil {
			return nil, err
		}
		return s.Connect(ctx, root, req.Remote, req.Credential)
	}
	var req struct {
		RepoID int64  `json:"repoId"`
		Number int    `json:"number"`
		Head   string `json:"head"`
		Body   string `json:"body"`
	}
	if err := decoder.Decode(&req); err != nil {
		return nil, err
	}
	if req.RepoID <= 0 {
		return nil, errors.New("repository ID is required")
	}
	repo, err := s.resolve(ctx, root, req.RepoID)
	if err != nil {
		return nil, err
	}
	return repo.postReview(ctx, req.Number, req.Head, req.Body)
}
