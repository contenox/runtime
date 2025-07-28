package githubapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/githubservice"
)

func AddGitHubRoutes(mux *http.ServeMux, cfg *serverops.Config, svc githubservice.Service) {
	mux.HandleFunc("POST /github/connect", connectRepo(svc))
	mux.HandleFunc("GET /github/repos", listRepos(svc))
	mux.HandleFunc("GET /github/repos/{repoID}/prs", listPRs(svc))
	mux.HandleFunc("DELETE /github/repos/{repoID}", disconnectRepo(svc))
	mux.HandleFunc("GET /github/repos/{repoID}/prs/{prNumber}", getPR(svc))
	mux.HandleFunc("POST /github/repos/{repoID}/prs/{prNumber}/comments", postComment(svc))
}

type connReq struct {
	UserID      string `json:"userID"`
	Owner       string `json:"owner"`
	RepoName    string `json:"repoName"`
	AccessToken string `json:"accessToken"`
	BotUserName string `json:"botUserName"`
}

func connectRepo(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		req, err := serverops.Decode[connReq](r)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}
		repo, err := svc.ConnectRepo(ctx, req.UserID, req.Owner, req.RepoName, req.AccessToken, req.BotUserName)
		if err != nil {
			serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}
		serverops.Encode(w, r, http.StatusCreated, repo)
	}
}

func listRepos(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repos, err := svc.ListRepos(ctx)
		if err != nil {
			serverops.Error(w, r, err, serverops.ListOperation)
			return
		}
		for _, ghr := range repos {
			ghr.AccessToken = "***-***"
		}
		serverops.Encode(w, r, http.StatusOK, repos)
	}
}

func listPRs(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := url.PathEscape(r.PathValue("repoID"))

		prs, err := svc.ListPRs(ctx, repoID)
		if err != nil {
			serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		serverops.Encode(w, r, http.StatusOK, prs)
	}
}

func disconnectRepo(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := url.PathEscape(r.PathValue("repoID"))

		if err := svc.DisconnectRepo(ctx, repoID); err != nil {
			serverops.Error(w, r, err, serverops.DeleteOperation)
			return
		}
		serverops.Encode(w, r, http.StatusNoContent, map[string]string{"message": "disconnected"})
	}
}

func getPR(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Get and validate repo ID
		repoID := url.PathEscape(r.PathValue("repoID"))
		if repoID == "" {
			serverops.Error(w, r, errors.New("repoID is required"), serverops.GetOperation)
			return
		}

		// Get and parse PR number
		prNumberStr := r.PathValue("prNumber")
		prNumber, err := strconv.Atoi(prNumberStr)
		if err != nil {
			serverops.Error(w, r, fmt.Errorf("invalid PR number: %w", err), serverops.GetOperation)
			return
		}

		// Fetch PR details
		details, err := svc.PR(ctx, repoID, prNumber)
		if err != nil {
			serverops.Error(w, r, err, serverops.GetOperation)
			return
		}

		serverops.Encode(w, r, http.StatusOK, details)
	}
}

type commentRequest struct {
	Comment string `json:"comment"`
}

func postComment(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Get repo ID
		repoID := url.PathEscape(r.PathValue("repoID"))
		if repoID == "" {
			serverops.Error(w, r, errors.New("repoID is required"), serverops.CreateOperation)
			return
		}

		// Get PR number
		prNumberStr := r.PathValue("prNumber")
		prNumber, err := strconv.Atoi(prNumberStr)
		if err != nil {
			serverops.Error(w, r, fmt.Errorf("invalid PR number: %w", err), serverops.CreateOperation)
			return
		}

		// Decode comment
		req, err := serverops.Decode[commentRequest](r)
		if err != nil {
			serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}

		// Validate comment
		if req.Comment == "" {
			serverops.Error(w, r, errors.New("comment cannot be empty"), serverops.CreateOperation)
			return
		}

		// Post comment
		if err := svc.PostComment(ctx, repoID, prNumber, req.Comment); err != nil {
			serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}

		serverops.Encode(w, r, http.StatusCreated, map[string]string{"status": "comment posted"})
	}
}
