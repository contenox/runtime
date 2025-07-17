package githubapi

import (
	"net/http"
	"net/url"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/githubservice"
)

func AddGitHubRoutes(mux *http.ServeMux, cfg *serverops.Config, svc githubservice.Service) {
	mux.HandleFunc("POST /github/connect", connectRepo(svc))
	mux.HandleFunc("GET /github/repos", listRepos(svc))
	mux.HandleFunc("GET /github/repos/{repoID}/prs", listPRs(svc))
	mux.HandleFunc("DELETE /github/repos/{repoID}", disconnectRepo(svc))
}

type connReq struct {
	UserID      string `json:"userID"`
	Owner       string `json:"owner"`
	RepoName    string `json:"repoName"`
	AccessToken string `json:"accessToken"`
}

func connectRepo(svc githubservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		req, err := serverops.Decode[connReq](r)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.CreateOperation)
			return
		}
		repo, err := svc.ConnectRepo(ctx, req.UserID, req.Owner, req.RepoName, req.AccessToken)
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
